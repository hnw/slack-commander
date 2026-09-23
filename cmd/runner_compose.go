package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"

	"github.com/hnw/compose-exec/compose"
)

type composeRunner struct {
	dir   string
	files []string

	once    sync.Once
	project *compose.Project
	loadErr error
}

// NewComposeRunner returns a runner backed by compose-exec.
// If dir is empty, it defaults to the current working directory.
func NewComposeRunner(dir string, files ...string) CommandRunner {
	dupFiles := append([]string(nil), files...)
	return &composeRunner{
		dir:   dir,
		files: dupFiles,
	}
}

func (r *composeRunner) load() {
	dir := strings.TrimSpace(r.dir)
	if dir == "" {
		wd, err := os.Getwd()
		if err != nil {
			r.loadErr = err
			return
		}
		dir = wd
	}
	project, err := compose.LoadProject(context.Background(), dir, r.files...)
	if err != nil {
		r.loadErr = err
		return
	}
	r.project = project
}

func (r *composeRunner) CommandContext(ctx context.Context, name string, arg ...string) Cmd {
	r.once.Do(r.load)
	if r.loadErr != nil {
		return &composeCmd{loadErr: r.loadErr}
	}
	if ctx == nil {
		panic("nil Context")
	}
	return &composeCmd{cmd: r.project.CommandContext(ctx, name, arg...), ctx: ctx}
}

type composeCmd struct {
	cmd     *compose.Cmd
	ctx     context.Context
	stderr  io.Writer
	loadErr error
}

func (c *composeCmd) SetStdin(r io.Reader) {
	if c.cmd != nil {
		c.cmd.Stdin = r
	}
}

func (c *composeCmd) SetStdout(w io.Writer) {
	if c.cmd != nil {
		c.cmd.Stdout = w
	}
}

func (c *composeCmd) SetStderr(w io.Writer) {
	if c.cmd != nil {
		c.cmd.Stderr = w
	}
	c.stderr = w
}

func (c *composeCmd) SetEnv(environment []string) {
	if c.cmd != nil {
		c.cmd.Env = mergeEnvironment(c.cmd.Env, environment)
	}
}

func (c *composeCmd) SetTTY() {
	if c.cmd != nil {
		c.cmd.TTY = true
	}
}

func (c *composeCmd) Run(timeout int) int {
	if code, invalid := c.validate(); invalid {
		return code
	}
	return c.finish(timeout, c.cmd.Run())
}

// RunWithStdin starts the compose command before exposing its stdin writer.
// The callback owns the writer after it is called.
func (c *composeCmd) RunWithStdin(timeout int, started func(io.WriteCloser)) int {
	if code, invalid := c.validate(); invalid {
		return code
	}
	stdin, err := c.cmd.StdinPipe()
	if err != nil {
		return c.finish(timeout, err)
	}
	if err := c.cmd.Start(); err != nil {
		return c.finish(timeout, err)
	}
	if started != nil {
		started(stdin)
	} else {
		_ = stdin.Close()
	}
	return c.finish(timeout, c.cmd.Wait())
}

func (c *composeCmd) validate() (int, bool) {
	if c.loadErr != nil {
		if c.stderr != nil {
			_, _ = fmt.Fprintf(c.stderr, "%v", c.loadErr)
		}
		return 127, true
	}
	if c.cmd == nil {
		if c.stderr != nil {
			_, _ = fmt.Fprintf(c.stderr, "Error: compose command is nil")
		}
		return 127, true
	}
	return 0, false
}

func (c *composeCmd) finish(timeout int, err error) int {
	if err == nil {
		return 0
	}
	var ee *compose.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode()
	}

	if c.ctx != nil {
		if errors.Is(c.ctx.Err(), context.Canceled) || errors.Is(err, context.Canceled) {
			return 143
		}
		if timeout > 0 && errors.Is(c.ctx.Err(), context.DeadlineExceeded) {
			if c.stderr != nil {
				_, _ = fmt.Fprintf(c.stderr, "Timeout exceeded (%ds)", timeout)
			}
			return 143
		}
	}

	if c.stderr != nil {
		_, _ = fmt.Fprintf(c.stderr, "Error: %v", err)
	}
	return 127
}

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
		if r == nil {
			return
		}
		// Avoid attaching stdin when there's no actual input.
		// An empty reader can cause the Docker attach stream to close early,
		// which drops stdout/stderr for compose-exec.
		if sr, ok := r.(*strings.Reader); ok && sr.Len() == 0 {
			return
		}
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

func (c *composeCmd) Run(timeout int) int {
	if c.loadErr != nil {
		if c.stderr != nil {
			_, _ = fmt.Fprintf(c.stderr, "%v", c.loadErr)
		}
		return 127
	}
	if c.cmd == nil {
		if c.stderr != nil {
			_, _ = fmt.Fprintf(c.stderr, "Error: compose command is nil")
		}
		return 127
	}

	err := c.cmd.Run()
	if err == nil {
		return 0
	}
	var ee *compose.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode()
	}

	if timeout > 0 && c.ctx != nil && errors.Is(c.ctx.Err(), context.DeadlineExceeded) {
		if c.stderr != nil {
			_, _ = fmt.Fprintf(c.stderr, "Timeout exceeded (%ds)", timeout)
		}
		return 143
	}

	if c.stderr != nil {
		_, _ = fmt.Fprintf(c.stderr, "Error: %v", err)
	}
	return 127
}

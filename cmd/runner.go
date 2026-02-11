package cmd

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"syscall"
	"time"
)

// Cmd is an executable command abstraction for different runners.
type Cmd interface {
	SetStdin(r io.Reader)
	SetStdout(w io.Writer)
	SetStderr(w io.Writer)
	Run(timeout int) int
}

// CommandRunner creates Cmd instances for a given command.
type CommandRunner interface {
	CommandContext(ctx context.Context, name string, arg ...string) Cmd
}

type execRunner struct{}

// NewExecRunner returns a runner backed by os/exec.
func NewExecRunner() CommandRunner {
	return &execRunner{}
}

func (r *execRunner) CommandContext(ctx context.Context, name string, arg ...string) Cmd {
	return &execCmd{cmd: exec.CommandContext(ctx, name, arg...)}
}

type execCmd struct {
	cmd *exec.Cmd
}

func (c *execCmd) SetStdin(r io.Reader) {
	c.cmd.Stdin = r
}

func (c *execCmd) SetStdout(w io.Writer) {
	c.cmd.Stdout = w
}

func (c *execCmd) SetStderr(w io.Writer) {
	c.cmd.Stderr = w
}

// Run executes the command and returns its exit code.
// Exit code meanings follow the previous behavior:
// - 0-255: actual exit code
// - 127: failed to start or unknown error
// - 143: terminated by signal or timeout
func (c *execCmd) Run(timeout int) int {
	c.cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	c.cmd.Cancel = func() error {
		// 参考: http://makiuchi-d.github.io/2020/05/10/go-kill-child-process.ja.html
		_ = syscall.Kill(-c.cmd.Process.Pid, syscall.SIGTERM) // setpgidしたPGIDはPIDと等しい
		time.Sleep(2 * time.Second)
		return syscall.Kill(-c.cmd.Process.Pid, syscall.SIGKILL)
	}

	if err := c.cmd.Start(); err != nil {
		if c.cmd.Stderr != nil {
			_, _ = fmt.Fprintf(c.cmd.Stderr, "%v", err)
		}
		return 127
	}

	err := c.cmd.Wait()
	if err != nil {
		if exitError, ok := err.(*exec.ExitError); ok {
			if exitError.ExitCode() == -1 {
				// https://pkg.go.dev/os#ProcessState.ExitCode
				// -1 if the process hasn't exited or was terminated by a signal.
				if c.cmd.Stderr != nil && timeout > 0 {
					_, _ = fmt.Fprintf(c.cmd.Stderr, "Timeout exceeded (%ds)", timeout)
				}
				return 143 // 128+15(SIGTERM)
			}
			return exitError.ExitCode()
		}
		if c.cmd.Stderr != nil {
			_, _ = fmt.Fprintf(c.cmd.Stderr, "Error: %v", err)
		}
		return 127
	}
	return c.cmd.ProcessState.ExitCode()
}

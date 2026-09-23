package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"syscall"
	"time"

	"github.com/creack/pty"
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
	return &execCmd{cmd: exec.CommandContext(ctx, name, arg...), ctx: ctx}
}

type execCmd struct {
	cmd    *exec.Cmd
	ctx    context.Context
	tty    bool
	stdout io.Writer
	stderr io.Writer
}

type ttyInputWriter struct {
	io.Writer
}

func (*ttyInputWriter) Close() error {
	return nil
}

func (c *execCmd) SetStdin(r io.Reader) {
	c.cmd.Stdin = r
}

func (c *execCmd) SetStdout(w io.Writer) {
	c.stdout = w
	c.cmd.Stdout = w
}

func (c *execCmd) SetStderr(w io.Writer) {
	c.stderr = w
	c.cmd.Stderr = w
}

func (c *execCmd) SetTTY() {
	c.tty = true
}

// Run executes the command and returns its exit code.
// Exit code meanings follow the previous behavior:
// - 0-255: actual exit code
// - 127: failed to start or unknown error
// - 143: terminated by signal or timeout
func (c *execCmd) Run(timeout int) int {
	return c.run(timeout, execStdin{})
}

// RunWithStdin は Start 後、Wait を妨げない入力処理の接続に writer を渡す。
// started は入力の完了を待たずに戻り、呼び出し側が endpoint を後始末する。
func (c *execCmd) RunWithStdin(timeout int, started func(io.WriteCloser)) int {
	return c.run(timeout, execStdin{onStarted: started})
}

func (c *execCmd) run(timeout int, stdin execStdin) int {
	if c.tty {
		return c.runTTY(timeout, stdin)
	}
	if err := stdin.prepare(c.cmd); err != nil {
		if c.cmd.Stderr != nil {
			_, _ = fmt.Fprint(c.cmd.Stderr, err)
		}
		return 127
	}
	defer stdin.closeUnclaimed()
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

	stdin.started()
	err := c.cmd.Wait()
	if err != nil {
		if exitError, ok := err.(*exec.ExitError); ok {
			if exitError.ExitCode() == -1 {
				// https://pkg.go.dev/os#ProcessState.ExitCode
				// -1 if the process hasn't exited or was terminated by a signal.
				if c.cmd.Stderr != nil && timeout > 0 && c.ctx != nil &&
					errors.Is(c.ctx.Err(), context.DeadlineExceeded) {
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

func (c *execCmd) runTTY(timeout int, stdin execStdin) int {
	defer stdin.closeUnclaimed()
	c.cmd.Stdin = nil
	c.cmd.Stdout = nil
	c.cmd.Stderr = nil
	c.cmd.Cancel = func() error {
		_ = syscall.Kill(-c.cmd.Process.Pid, syscall.SIGTERM)
		time.Sleep(2 * time.Second)
		return syscall.Kill(-c.cmd.Process.Pid, syscall.SIGKILL)
	}

	terminal, err := pty.StartWithSize(c.cmd, &pty.Winsize{Rows: 24, Cols: 80})
	if err != nil {
		if c.stderr != nil {
			_, _ = fmt.Fprint(c.stderr, err)
		}
		return 127
	}
	defer func() { _ = terminal.Close() }()

	stdout := c.stdout
	if stdout == nil {
		stdout = io.Discard
	}
	outputDone := make(chan struct{})
	go func() {
		_, _ = io.Copy(stdout, terminal)
		close(outputDone)
	}()

	if stdin.onStarted != nil {
		stdin.unclaimed = &ttyInputWriter{Writer: terminal}
		stdin.started()
	}
	err = c.cmd.Wait()
	_ = terminal.Close()
	<-outputDone
	return c.exitCode(timeout, err, c.stderr)
}

func (c *execCmd) exitCode(timeout int, err error, stderr io.Writer) int {
	if err != nil {
		if exitError, ok := err.(*exec.ExitError); ok {
			if exitError.ExitCode() == -1 {
				if stderr != nil && timeout > 0 && c.ctx != nil &&
					errors.Is(c.ctx.Err(), context.DeadlineExceeded) {
					_, _ = fmt.Fprintf(stderr, "Timeout exceeded (%ds)", timeout)
				}
				return 143
			}
			return exitError.ExitCode()
		}
		if stderr != nil {
			_, _ = fmt.Fprintf(stderr, "Error: %v", err)
		}
		return 127
	}
	return c.cmd.ProcessState.ExitCode()
}

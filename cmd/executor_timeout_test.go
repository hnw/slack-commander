package cmd

import (
	"context"
	"errors"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

func TestRunMatchedCommandTimeoutMessage(t *testing.T) {
	for _, tty := range []bool{false, true} {
		for _, outcome := range []string{"timeout", "cancel", "nonzero", "signal"} {
			t.Run(outcome+map[bool]string{false: "/plain", true: "/tty"}[tty], func(t *testing.T) {
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				var runner CommandRunner
				wantCode := 143
				if outcome == "nonzero" || outcome == "signal" {
					wantCode = 7
					if outcome == "signal" {
						wantCode = 143
					}
					runner = singleCmdRunner{command: &fakeCmd{exitCode: wantCode}}
				} else {
					runner = &blockingRunner{}
					if outcome == "cancel" {
						cancel()
					}
				}
				command := testRuntimeCommand(&testExecutionConfig{Timeout: 10 * time.Millisecond, TTY: tty}, runner)
				wq := make(chan *CommandOutput, 10)
				if code := runMatchedCommand(ctx, command, []string{"test"}, "", &CommandInput{}, wq, nil); code != wantCode {
					t.Fatalf("code = %d, want %d", code, wantCode)
				}
				var text strings.Builder
				for _, output := range drainOutputs(wq) {
					text.WriteString(output.Text)
				}
				want := ""
				if outcome == "timeout" {
					want = "Timeout exceeded (10ms)"
				}
				if got := text.String(); got != want {
					t.Fatalf("output = %q, want %q", got, want)
				}
			})
		}
	}
}

type completedContextRunner struct{ ctx context.Context }

func (r *completedContextRunner) CommandContext(ctx context.Context, _ string, _ ...string) Cmd {
	r.ctx = ctx
	return &fakeCmd{stdoutText: "done"}
}

func TestRunMatchedCommandCancelsBeforeFlushingOutput(t *testing.T) {
	for _, tty := range []bool{false, true} {
		t.Run(map[bool]string{false: "plain", true: "tty"}[tty], func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				runner := &completedContextRunner{}
				command := testRuntimeCommand(&testExecutionConfig{
					Timeout: time.Second, TTY: tty, OutputFlushInterval: time.Hour,
				}, runner)
				wq := make(chan *CommandOutput)
				done := make(chan int, 1)
				go func() {
					done <- runMatchedCommand(context.Background(), command, []string{"test"}, "", &CommandInput{}, wq, nil)
				}()
				synctest.Wait()
				if cause := context.Cause(runner.ctx); !errors.Is(cause, context.Canceled) {
					t.Errorf("cause before flushing = %v, want context.Canceled", cause)
				}
				time.Sleep(2 * time.Second)
				if cause := context.Cause(runner.ctx); !errors.Is(cause, context.Canceled) {
					t.Errorf("cause after deadline = %v, want context.Canceled", cause)
				}
				if output := <-wq; output.Text != "done" {
					t.Errorf("output = %q, want done", output.Text)
				}
				if code := <-done; code != 0 {
					t.Fatalf("code = %d, want 0", code)
				}
			})
		})
	}
}

func TestRunMatchedCommandParentDeadlineDoesNotReportCommandTimeout(t *testing.T) {
	for _, tty := range []bool{false, true} {
		for _, timeout := range []time.Duration{0, 2 * time.Second} {
			t.Run(timeout.String()+map[bool]string{false: "/plain", true: "/tty"}[tty], func(t *testing.T) {
				synctest.Test(t, func(t *testing.T) {
					ctx, cancel := context.WithTimeout(context.Background(), time.Second)
					defer cancel()
					command := testRuntimeCommand(&testExecutionConfig{Timeout: timeout, TTY: tty}, &blockingRunner{})
					wq := make(chan *CommandOutput, 10)
					if code := runMatchedCommand(ctx, command, []string{"test"}, "", &CommandInput{}, wq, nil); code != 143 {
						t.Fatalf("code = %d, want 143", code)
					}
					for _, output := range drainOutputs(wq) {
						if output.Text != "" {
							t.Fatalf("output = %q, want empty", output.Text)
						}
					}
				})
			})
		}
	}
}

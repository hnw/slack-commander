package cmd

import (
	"context"
	"io"
	"testing"
	"time"
)

type blockingRunner struct {
	started chan struct{}
}

func (r *blockingRunner) CommandContext(ctx context.Context, _ string, _ ...string) Cmd {
	return &blockingCmd{ctx: ctx, started: r.started}
}

type blockingCmd struct {
	ctx     context.Context
	started chan struct{}
}

func (c *blockingCmd) SetStdin(_ io.Reader)  {}
func (c *blockingCmd) SetStdout(_ io.Writer) {}
func (c *blockingCmd) SetStderr(_ io.Writer) {}

func (c *blockingCmd) Run(_ int) int {
	if c.started != nil {
		select {
		case c.started <- struct{}{}:
		default:
		}
	}
	<-c.ctx.Done()
	return 143
}

func TestExecutorCancelStopsRunningCommand(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	wq := make(chan *CommandOutput, 10)
	started := make(chan struct{}, 1)
	runner := &blockingRunner{started: started}
	cfgs := []*testCommandConfig{
		newTestCommandConfig(&testExecutionConfig{Keyword: "date", Command: "date"}),
	}

	commandSet := testCommandSet(cfgs, func(*testExecutionConfig) CommandRunner { return runner })
	executor := NewExecutor(wq)
	done := make(chan struct{})
	go func() {
		input := &CommandInput{Text: "date", AllowedCommandIndexes: []int{0}}
		input.ResolvedInput = commandSet.ResolveInput(input.Text, input.AllowedCommandIndexes)
		executor.Execute(ctx, input, nil)
		close(done)
	}()

	select {
	case <-started:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("command did not start")
	}

	cancel()

	select {
	case <-done:
	case <-time.After(1 * time.Second):
		t.Fatal("executor did not stop after cancel")
	}

	outputs := drainOutputs(wq)
	var gotExit int
	for _, out := range outputs {
		if out.Finished {
			gotExit = out.ExitCode
			break
		}
	}
	if gotExit != 143 {
		t.Fatalf("expected exit code 143 after cancel, got %d", gotExit)
	}
}

func TestExecutorTimeoutCancelsCommand(t *testing.T) {
	wq := make(chan *CommandOutput, 10)
	started := make(chan struct{}, 1)
	runner := &blockingRunner{started: started}

	cfgs := []*testCommandConfig{
		newTestCommandConfig(&testExecutionConfig{Keyword: "date", Command: "date", Timeout: 1}),
	}

	commandSet := testCommandSet(cfgs, func(*testExecutionConfig) CommandRunner { return runner })
	executor := NewExecutor(wq)
	done := make(chan struct{})
	go func() {
		input := &CommandInput{Text: "date", AllowedCommandIndexes: []int{0}}
		input.ResolvedInput = commandSet.ResolveInput(input.Text, input.AllowedCommandIndexes)
		executor.Execute(context.Background(), input, nil)
		close(done)
	}()

	select {
	case <-started:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("command did not start")
	}

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("executor did not stop after timeout")
	}

	outputs := drainOutputs(wq)
	var gotExit int
	for _, out := range outputs {
		if out.Finished {
			gotExit = out.ExitCode
			break
		}
	}
	if gotExit != 143 {
		t.Fatalf("expected exit code 143 after timeout, got %d", gotExit)
	}
}

package cmd

import (
	"bufio"
	"context"
	"io"
	"strings"
	"testing"
	"time"
)

type liveTestCmd struct {
	started chan struct{}
	read    chan struct{}
	lines   chan string
}

func TestExecutorLiveInputStartFailureAndFiniteFallback(t *testing.T) {
	var registry LiveInputRegistry
	conversation := ConversationContext{ChannelID: "C", RootThreadTimestamp: "1"}
	key := ThreadKey{ChannelID: "C", RootThreadTimestamp: "1"}
	command := NewExecRunner().CommandContext(context.Background(), "/no-such-live-command")
	if code := runWithInput(command, 0, "initial", conversation, &registry); code != 127 {
		t.Fatalf("code=%d", code)
	}
	if registry.Lookup(key) != nil {
		t.Fatal("published after failed start")
	}
	finite := &fakeCmd{}
	if code := runWithInput(finite, 0, "no-final-newline", conversation, &registry); code != 0 {
		t.Fatalf("code=%d", code)
	}
	got, err := io.ReadAll(finite.stdin)
	if err != nil || string(got) != "no-final-newline" || registry.Lookup(key) != nil {
		t.Fatalf("finite input changed: %q err=%v", got, err)
	}
	for _, runner := range []CommandRunner{NewHTTPRunner(nil), NewComposeRunner("")} {
		c := runner.CommandContext(context.Background(), "unused")
		if _, live := c.(interface {
			RunLive(int, func(io.WriteCloser) func()) int
		}); live {
			t.Fatalf("non-exec runner %T gained live input", runner)
		}
	}
}

func waitForLiveInput(t *testing.T, registry *LiveInputRegistry, key ThreadKey) *LiveInput {
	t.Helper()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	timeout := time.NewTimer(3 * time.Second)
	defer timeout.Stop()
	for {
		if endpoint := registry.Lookup(key); endpoint != nil {
			return endpoint
		}
		select {
		case <-ticker.C:
		case <-timeout.C:
			t.Fatal("live input not published")
		}
	}
}

func TestExecutorLiveInputRemovesOnTimeoutAndCancel(t *testing.T) {
	for _, timeout := range []bool{false, true} {
		duration := 5 * time.Second
		if timeout {
			duration = time.Second
		}
		ctx, cancel := context.WithTimeout(context.Background(), duration)
		t.Cleanup(cancel)
		var registry LiveInputRegistry
		key := ThreadKey{ChannelID: "C", RootThreadTimestamp: "1"}
		command := NewExecRunner().CommandContext(ctx, "/bin/sh", "-c", "read value")
		done := make(chan int, 1)
		go func() {
			done <- runWithInput(command, 1, "", ConversationContext{ChannelID: "C", RootThreadTimestamp: "1"}, &registry)
		}()
		endpoint := waitForLiveInput(t, &registry, key)
		if !timeout {
			cancel()
		}
		select {
		case code := <-done:
			if code != 143 {
				t.Fatalf("code=%d", code)
			}
		case <-time.After(6 * time.Second):
			t.Fatal("executor stuck")
		}
		if registry.Lookup(key) != nil {
			t.Fatal("registration survived cancellation")
		}
		if err := endpoint.TrySend("late"); err != ErrLiveInputClosed {
			t.Fatalf("err=%v", err)
		}
	}
}

type discardCloser struct{ io.Writer }

func (discardCloser) Close() error { return nil }

type immediateLiveCmd struct{ fakeCmd }

func (*immediateLiveCmd) RunLive(_ int, started func(io.WriteCloser) func()) int {
	cleanup := started(discardCloser{io.Discard})
	cleanup()
	return 0
}

type chainLiveRunner struct {
	t        *testing.T
	registry *LiveInputRegistry
	key      ThreadKey
	calls    int
}

func (r *chainLiveRunner) CommandContext(context.Context, string, ...string) Cmd {
	r.calls++
	if r.registry.Lookup(r.key) != nil {
		r.t.Error("previous process remained registered between commands")
	}
	return &immediateLiveCmd{}
}

func TestExecutorLiveInputPreservesChainAndUnregistersBetweenProcesses(t *testing.T) {
	var registry LiveInputRegistry
	runner := &chainLiveRunner{
		t:        t,
		registry: &registry,
		key:      ThreadKey{ChannelID: "C", RootThreadTimestamp: "1"},
	}
	rq := make(chan *CommandInput, 1)
	rq <- &CommandInput{Text: "agent && agent || agent ; agent", ConversationContext: ConversationContext{ChannelID: "C", RootThreadTimestamp: "1"}}
	close(rq)
	cfg := NewCommandConfig(&Definition{Keyword: "agent", Command: "agent"}, nil)
	ExecutorWithLiveInput(
		context.Background(),
		rq,
		make(chan *CommandOutput, 10),
		[]*CommandConfig{cfg},
		func(*CommandConfig) CommandRunner { return runner },
		&registry,
	)
	if runner.calls != 3 {
		t.Fatalf("calls=%d", runner.calls)
	}
	if registry.Lookup(runner.key) != nil {
		t.Fatal("last registration remained")
	}
}

func TestLiveExecExitsWhileInitialWriteIsBlocked(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var registry LiveInputRegistry
	command := NewExecRunner().CommandContext(ctx, "/bin/sh", "-c", "exit 0")
	code := runWithInput(
		command,
		0,
		strings.Repeat("x", 1<<20),
		ConversationContext{ChannelID: "C", RootThreadTimestamp: "1"},
		&registry,
	)
	if code != 0 {
		t.Fatalf("code=%d", code)
	}
}

func (*liveTestCmd) SetStdin(io.Reader)  {}
func (*liveTestCmd) SetStdout(io.Writer) {}
func (*liveTestCmd) SetStderr(io.Writer) {}
func (*liveTestCmd) Run(int) int         { return 99 }
func (c *liveTestCmd) RunLive(_ int, started func(io.WriteCloser) func()) int {
	r, w := io.Pipe()
	defer func() { _ = r.Close() }()
	cleanup := started(w)
	defer cleanup()
	close(c.started)
	<-c.read
	reader := bufio.NewReader(r)
	for range 2 {
		line, err := reader.ReadString('\n')
		if err != nil {
			return 127
		}
		c.lines <- line
	}
	return 0
}

type singleCmdRunner struct{ command Cmd }

func (r singleCmdRunner) CommandContext(context.Context, string, ...string) Cmd { return r.command }

func TestExecutorLiveInputPublishesAfterInitialIsOrdered(t *testing.T) {
	for _, continuation := range []bool{false, true} {
		c := &liveTestCmd{
			started: make(chan struct{}),
			read:    make(chan struct{}),
			lines:   make(chan string, 2),
		}
		var registry LiveInputRegistry
		key := ThreadKey{ChannelID: "C", RootThreadTimestamp: "1"}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		rq := make(chan *CommandInput, 1)
		wq := make(chan *CommandOutput, 10)
		cfg := NewCommandConfig(
			&Definition{Keyword: "agent", Command: "agent", Continuation: ContinuationThread},
			nil,
		)
		rq <- &CommandInput{
			Text: "agent\ninitial", ThreadContinuation: continuation,
			ConversationContext: ConversationContext{ChannelID: "C", RootThreadTimestamp: "1"},
		}
		close(rq)
		done := make(chan struct{})
		go func() {
			ExecutorWithLiveInput(
				ctx,
				rq,
				wq,
				[]*CommandConfig{cfg},
				func(*CommandConfig) CommandRunner {
					return singleCmdRunner{c}
				},
				&registry,
			)
			close(done)
		}()
		select {
		case <-c.started:
		case <-ctx.Done():
			t.Fatal("not started")
		}
		endpoint := registry.Lookup(key)
		if endpoint == nil {
			t.Fatal("not registered")
		}
		if err := endpoint.TrySend("reply"); err != nil {
			t.Fatal(err)
		}
		close(c.read)
		select {
		case <-done:
		case <-ctx.Done():
			t.Fatal("did not finish")
		}
		cancel()
		if got := <-c.lines; got != "initial\n" {
			t.Fatal(got)
		}
		if got := <-c.lines; got != "reply\n" {
			t.Fatal(got)
		}
		if registry.Lookup(key) != nil {
			t.Fatal("registration survived exit")
		}
	}
}

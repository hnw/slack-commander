package cmd

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

type stdinTestCmd struct {
	started chan struct{}
	read    chan struct{}
	lines   chan string
}

func TestExecSessionEOF(t *testing.T) {
	for _, interactive := range []bool{false, true} {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		var registry testThreadRegistry
		conversation := ConversationID{}
		want := "raw input"
		if interactive {
			conversation = ConversationID{ChannelID: "C", RootTimestamp: "1"}
			want += "\n"
		}
		command := NewExecRunner().CommandContext(ctx, "/bin/cat")
		var out bytes.Buffer
		command.SetStdout(&out)
		code := testRunWithInput(command, 5, 30*time.Millisecond, "raw input", conversation, &registry)
		cancel()
		if code != 0 || out.String() != want {
			t.Fatalf("interactive=%v code=%d output=%q", interactive, code, out.String())
		}
		if registry.lookup(ConversationID{ChannelID: "C", RootTimestamp: "1"}) != nil {
			t.Fatal("stale endpoint")
		}
	}
}

type eofTestCmd struct {
	stdinTestCmd
	afterEOF func()
}

func (c *eofTestCmd) RunWithStdin(_ time.Duration, started func(io.WriteCloser)) int {
	r, w := io.Pipe()
	defer func() { _ = r.Close() }()
	started(w)
	_, _ = io.ReadAll(r)
	c.afterEOF()
	return 0
}

func TestExecutorIdleUnregistersBeforeProcessExit(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var registry testThreadRegistry
		key := ConversationID{ChannelID: "C", RootTimestamp: "1"}
		command := &eofTestCmd{afterEOF: func() {
			synctest.Wait()
			if registry.lookup(key) != nil {
				t.Fatal("EOF left a registered endpoint while process runs")
			}
		}}
		if code := testRunWithInput(
			command,
			0,
			time.Second,
			"",
			ConversationID{ChannelID: "C", RootTimestamp: "1"},
			&registry,
		); code != 0 {
			t.Fatal(code)
		}
	})
}

func TestExecutorFiniteCanExitWithoutConsumingStdin(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	command := NewExecRunner().CommandContext(ctx, "/bin/sh", "-c", "exit 0")
	if code := testRunWithInput(
		command,
		0,
		0,
		strings.Repeat("x", 1<<20),
		ConversationID{},
		nil,
	); code != 0 {
		t.Fatal(code)
	}
}

func TestExecutorStdinStartFailureAndFiniteFallback(t *testing.T) {
	var registry testThreadRegistry
	conversation := ConversationID{ChannelID: "C", RootTimestamp: "1"}
	key := ConversationID{ChannelID: "C", RootTimestamp: "1"}
	command := NewExecRunner().CommandContext(context.Background(), "/no-such-live-command")
	if code := testRunWithInput(command, 0, 0, "initial", conversation, &registry); code != 127 {
		t.Fatalf("code=%d", code)
	}
	if registry.lookup(key) != nil {
		t.Fatal("published after failed start")
	}
	finite := &fakeCmd{}
	if code := testRunWithInput(finite, 0, 0, "no-final-newline", conversation, &registry); code != 0 {
		t.Fatalf("code=%d", code)
	}
	got, err := io.ReadAll(finite.stdin)
	if err != nil || string(got) != "no-final-newline" || registry.lookup(key) != nil {
		t.Fatalf("finite input changed: %q err=%v", got, err)
	}
	compose := NewComposeRunner("").CommandContext(context.Background(), "unused")
	if _, live := compose.(interface {
		RunWithStdin(time.Duration, func(io.WriteCloser)) int
	}); !live {
		t.Fatalf("compose runner %T lacks interactive stdin", compose)
	}
	http := NewHTTPRunner(RunnerConfig{}).CommandContext(context.Background(), "unused")
	if _, live := http.(interface {
		RunWithStdin(time.Duration, func(io.WriteCloser)) int
	}); live {
		t.Fatalf("http runner %T gained interactive stdin", http)
	}
}

func waitForInteractiveStdin(
	t *testing.T,
	registry *testThreadRegistry,
	key ConversationID,
) *InteractiveStdin {
	t.Helper()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	timeout := time.NewTimer(3 * time.Second)
	defer timeout.Stop()
	for {
		if endpoint := registry.lookup(key); endpoint != nil {
			return endpoint
		}
		select {
		case <-ticker.C:
		case <-timeout.C:
			t.Fatal("interactive stdin not published")
		}
	}
}

func TestExecutorStdinRemovesOnTimeoutAndCancel(t *testing.T) {
	for _, timeout := range []bool{false, true} {
		duration := 5 * time.Second
		if timeout {
			duration = time.Second
		}
		ctx, cancel := context.WithTimeout(context.Background(), duration)
		t.Cleanup(cancel)
		var registry testThreadRegistry
		key := ConversationID{ChannelID: "C", RootTimestamp: "1"}
		command := NewExecRunner().CommandContext(ctx, "/bin/sh", "-c", "read value")
		done := make(chan int, 1)
		go func() {
			done <- testRunWithInput(command, 1, 0, "", ConversationID{ChannelID: "C", RootTimestamp: "1"}, &registry)
		}()
		endpoint := waitForInteractiveStdin(t, &registry, key)
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
		if registry.lookup(key) != nil {
			t.Fatal("registration survived cancellation")
		}
		if err := endpoint.TrySend("late"); err != ErrInteractiveStdinClosed {
			t.Fatalf("err=%v", err)
		}
	}
}

func TestInteractiveExecCanExitWithoutConsumingStdin(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var registry testThreadRegistry
	command := NewExecRunner().CommandContext(ctx, "/bin/sh", "-c", "exit 0")
	code := testRunWithInput(
		command,
		0,
		0,
		strings.Repeat("x", 1<<20),
		ConversationID{ChannelID: "C", RootTimestamp: "1"},
		&registry,
	)
	if code != 0 {
		t.Fatalf("code=%d", code)
	}
}

func TestTTYCommandNormalizesMergedOutputAndRoutesThreadInput(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var registry testThreadRegistry
	key := ConversationID{ChannelID: "C", RootTimestamp: "1"}
	command := testRuntimeCommand(&testExecutionConfig{TTY: true}, NewExecRunner())
	outputs := make(chan *CommandOutput, 10)
	done := make(chan int, 1)
	go func() {
		done <- runMatchedCommand(
			ctx,
			command,
			[]string{
				"/bin/sh",
				"-c",
				"IFS= read -r first; IFS= read -r second; printf '\\033[31m%s|%s\\033[0m\\r\\n' \"$first\" \"$second\"; printf ERR >&2",
			},
			"initial\n",
			&CommandInput{ConversationID: ConversationID(key)},
			outputs,
			testLifecycle{registry: &registry, conversation: ConversationID(key)},
		)
	}()

	endpoint := waitForInteractiveStdin(t, &registry, key)
	if err := endpoint.TrySend("reply\n"); err != nil {
		t.Fatal(err)
	}
	select {
	case code := <-done:
		if code != 0 {
			t.Fatalf("code=%d", code)
		}
	case <-ctx.Done():
		t.Fatal("TTY command did not finish")
	}
	if registry.lookup(key) != nil {
		t.Fatal("TTY endpoint survived command exit")
	}

	var text string
	for _, output := range drainOutputs(outputs) {
		if output.IsErrOut {
			t.Fatal("TTY output was classified as stderr")
		}
		text += output.Text
	}
	if strings.ContainsAny(text, "\x1b\r") ||
		!strings.Contains(text, "initial|reply") || !strings.Contains(text, "ERR") {
		t.Fatalf("TTY output=%q", text)
	}
}

type stdinCaptureCmd struct {
	want     int
	captured []byte
}

func (*stdinCaptureCmd) SetStdin(io.Reader)    {}
func (*stdinCaptureCmd) SetStdout(io.Writer)   {}
func (*stdinCaptureCmd) SetStderr(io.Writer)   {}
func (*stdinCaptureCmd) Run(time.Duration) int { return 99 }
func (c *stdinCaptureCmd) RunWithStdin(_ time.Duration, started func(io.WriteCloser)) int {
	r, w := io.Pipe()
	defer func() { _ = r.Close() }()
	started(w)
	c.captured = make([]byte, c.want)
	if _, err := io.ReadFull(r, c.captured); err != nil {
		return 127
	}
	return 0
}

func TestTTYCommandTerminatesInitialAndReplyWithCR(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	const expected = "initial\rreply\r"
	capture := &stdinCaptureCmd{want: len(expected)}
	var registry testThreadRegistry
	key := ConversationID{ChannelID: "C", RootTimestamp: "1"}
	command := testRuntimeCommand(&testExecutionConfig{TTY: true}, singleCmdRunner{command: capture})
	done := make(chan int, 1)
	go func() {
		done <- runMatchedCommand(
			ctx,
			command,
			[]string{"unused"},
			"initial",
			&CommandInput{ConversationID: ConversationID(key)},
			make(chan *CommandOutput, 1),
			testLifecycle{registry: &registry, conversation: ConversationID(key)},
		)
	}()
	endpoint := waitForInteractiveStdin(t, &registry, key)
	if err := endpoint.TrySend("reply"); err != nil {
		t.Fatal(err)
	}
	select {
	case code := <-done:
		if code != 0 {
			t.Fatalf("code=%d", code)
		}
	case <-ctx.Done():
		t.Fatal("TTY command did not finish")
	}
	if got := string(capture.captured); got != expected {
		t.Fatalf("stdin=%q, want %q", got, expected)
	}
}

func (*stdinTestCmd) SetStdin(io.Reader)    {}
func (*stdinTestCmd) SetStdout(io.Writer)   {}
func (*stdinTestCmd) SetStderr(io.Writer)   {}
func (*stdinTestCmd) Run(time.Duration) int { return 99 }
func (c *stdinTestCmd) RunWithStdin(_ time.Duration, started func(io.WriteCloser)) int {
	r, w := io.Pipe()
	defer func() { _ = r.Close() }()
	started(w)
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

type lifecycleSwitchRunner struct {
	started  chan struct{}
	finish   chan struct{}
	exitCode int
}

func (r *lifecycleSwitchRunner) CommandContext(context.Context, string, ...string) Cmd {
	return lifecycleSwitchCmd{runner: r}
}

type lifecycleSwitchCmd struct{ runner *lifecycleSwitchRunner }

func (lifecycleSwitchCmd) SetStdin(io.Reader)    {}
func (lifecycleSwitchCmd) SetStdout(io.Writer)   {}
func (lifecycleSwitchCmd) SetStderr(io.Writer)   {}
func (lifecycleSwitchCmd) Run(time.Duration) int { return 0 }
func (c lifecycleSwitchCmd) RunWithStdin(_ time.Duration, start func(io.WriteCloser)) int {
	reader, writer := io.Pipe()
	defer func() { _ = reader.Close(); _ = writer.Close() }()
	start(writer)
	close(c.runner.started)
	select {
	case <-c.runner.finish:
		return c.runner.exitCode
	case <-time.After(5 * time.Second):
		return 124
	}
}

func TestExecutorSwitchesActiveStdinReplyCommandWithinChain(t *testing.T) {
	store := &StdinStore{}
	conversation := ConversationID{ChannelID: "C", RootTimestamp: "1"}
	firstRunner := &lifecycleSwitchRunner{started: make(chan struct{}), finish: make(chan struct{})}
	secondRunner := &lifecycleSwitchRunner{started: make(chan struct{}), finish: make(chan struct{})}
	makeReply := func(index int) *Command {
		return NewCommand(CommandConfig{
			Index: index, MatcherConfig: MatcherConfig{RawMatcherConfig: RawMatcherConfig{Keyword: "*"}},
			ParserConfig: ParserConfig{InputBodyMode: InputBodyRawStdin},
			RunnerConfig: RunnerConfig{RawRunnerConfig: RawRunnerConfig{Runner: RunnerStdinReply, Command: "stdin-reply"}},
			Dispatch:     DispatchQueue,
		}, nil, nil)
	}
	firstReply, secondReply := makeReply(2), makeReply(3)
	first := NewCommand(CommandConfig{
		Index: 0, MatcherConfig: MatcherConfig{RawMatcherConfig: RawMatcherConfig{Keyword: "first"}},
		ParserConfig: ParserConfig{AllowInChain: true}, ExecutorConfig: ExecutorConfig{InteractiveStdin: true},
		RunnerConfig: RunnerConfig{RawRunnerConfig: RawRunnerConfig{Command: "first"}}, Dispatch: DispatchQueue,
	}, firstRunner, NewCommandSet([]*Command{firstReply}))
	second := NewCommand(CommandConfig{
		Index: 1, MatcherConfig: MatcherConfig{RawMatcherConfig: RawMatcherConfig{Keyword: "second"}},
		ParserConfig: ParserConfig{AllowInChain: true}, ExecutorConfig: ExecutorConfig{InteractiveStdin: true},
		RunnerConfig: RunnerConfig{RawRunnerConfig: RawRunnerConfig{Command: "second"}}, Dispatch: DispatchQueue,
	}, secondRunner, NewCommandSet([]*Command{secondReply}))
	commands := NewCommandSet([]*Command{first, second})
	var replies []*CommandInput
	router := NewConversationRouter(store, commands, nil, newTestDispatcher(func(input *CommandInput) bool {
		if input.MessageID.Timestamp != conversation.RootTimestamp {
			replies = append(replies, input)
		}
		return true
	}), 2)
	root := &CommandInput{Text: "first ; unknown ; second", ConversationID: conversation, MessageID: MessageID{Timestamp: conversation.RootTimestamp}, AllowedCommandIndexes: []int{0, 1}}
	if result, err := router.Accept(root); err != nil || result != AcceptRouted {
		t.Fatalf("root = %v, %v; want routed", result, err)
	}
	done := make(chan struct{})
	go func() {
		NewExecutor(make(chan *CommandOutput, 10)).Execute(context.Background(), root, store.Lifecycle(conversation))
		close(done)
	}()
	waitForLifecycleSignal(t, firstRunner.started, "stdin command did not start")
	assertActiveReplyCommand(t, store, conversation, firstReply)
	if got := acceptStdinReply(t, router, conversation, 2, "reply-a"); got != AcceptRouted {
		t.Fatalf("first reply ACL = %v, want routed", got)
	}
	if got := acceptStdinReply(t, router, conversation, 3, "reply-b-denied"); got != AcceptIgnored {
		t.Fatalf("second reply ACL while first is active = %v, want ignored", got)
	}
	close(firstRunner.finish)
	waitForLifecycleSignal(t, secondRunner.started, "second stdin command did not start")
	assertActiveReplyCommand(t, store, conversation, secondReply)
	if got := acceptStdinReply(t, router, conversation, 2, "reply-a-denied"); got != AcceptIgnored {
		t.Fatalf("first reply ACL while second is active = %v, want ignored", got)
	}
	if got := acceptStdinReply(t, router, conversation, 3, "reply-b"); got != AcceptRouted {
		t.Fatalf("second reply ACL = %v, want routed", got)
	}
	close(secondRunner.finish)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("executor did not finish")
	}
	if len(replies) != 2 || replies[0].ResolvedInput.Commands[0].Command != firstReply || replies[1].ResolvedInput.Commands[0].Command != secondReply {
		t.Fatalf("routed reply commands = %+v, want first then second", replies)
	}
}

func waitForLifecycleSignal(t *testing.T, signal <-chan struct{}, failure string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(time.Second):
		t.Fatal(failure)
	}
}

func assertActiveReplyCommand(t *testing.T, store *StdinStore, conversation ConversationID, want *Command) {
	t.Helper()
	entry, ok := store.lookup(conversation)
	if !ok || entry.implicitReplyCommand != want {
		t.Fatalf("active entry = (%+v,%v), want reply %p", entry, ok, want)
	}
}

func acceptStdinReply(t *testing.T, router *ConversationRouter, conversation ConversationID, index int, timestamp string) AcceptResult {
	t.Helper()
	input := &CommandInput{Text: "body", ConversationID: conversation, MessageID: MessageID{Timestamp: timestamp}, AllowedCommandIndexes: []int{index}}
	result, err := router.Accept(input)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestExecutorDoesNotRegisterSkippedStdinCommands(t *testing.T) {
	for _, tc := range []struct {
		name     string
		chain    string
		exitCode int
	}{
		{name: "failed and skips second", chain: "first && second", exitCode: 1},
		{name: "successful or skips second", chain: "first || second", exitCode: 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &StdinStore{}
			conversation := ConversationID{ChannelID: "C", RootTimestamp: "1"}
			firstRunner := &lifecycleSwitchRunner{started: make(chan struct{}), finish: make(chan struct{}), exitCode: tc.exitCode}
			secondRunner := &lifecycleSwitchRunner{started: make(chan struct{}), finish: make(chan struct{})}
			makeReply := func(index int) *Command {
				return NewCommand(CommandConfig{Index: index, MatcherConfig: MatcherConfig{RawMatcherConfig: RawMatcherConfig{Keyword: "*"}}, RunnerConfig: RunnerConfig{RawRunnerConfig: RawRunnerConfig{Command: "stdin-reply"}}, Dispatch: DispatchQueue}, nil, nil)
			}
			first := NewCommand(CommandConfig{Index: 0, MatcherConfig: MatcherConfig{RawMatcherConfig: RawMatcherConfig{Keyword: "first"}}, ParserConfig: ParserConfig{AllowInChain: true}, ExecutorConfig: ExecutorConfig{InteractiveStdin: true}, RunnerConfig: RunnerConfig{RawRunnerConfig: RawRunnerConfig{Command: "first"}}, Dispatch: DispatchQueue}, firstRunner, NewCommandSet([]*Command{makeReply(2)}))
			second := NewCommand(CommandConfig{Index: 1, MatcherConfig: MatcherConfig{RawMatcherConfig: RawMatcherConfig{Keyword: "second"}}, ParserConfig: ParserConfig{AllowInChain: true}, ExecutorConfig: ExecutorConfig{InteractiveStdin: true}, RunnerConfig: RunnerConfig{RawRunnerConfig: RawRunnerConfig{Command: "second"}}, Dispatch: DispatchQueue}, secondRunner, NewCommandSet([]*Command{makeReply(3)}))
			commands := NewCommandSet([]*Command{first, second})
			router := NewConversationRouter(store, commands, nil, newTestDispatcher(func(*CommandInput) bool { return true }), 1)
			root := &CommandInput{Text: tc.chain, ConversationID: conversation, MessageID: MessageID{Timestamp: "1"}, AllowedCommandIndexes: []int{0, 1}}
			if result, err := router.Accept(root); err != nil || result != AcceptRouted {
				t.Fatalf("root = %v, %v", result, err)
			}
			done := make(chan struct{})
			go func() {
				NewExecutor(make(chan *CommandOutput, 10)).Execute(context.Background(), root, store.Lifecycle(conversation))
				close(done)
			}()
			select {
			case <-firstRunner.started:
			case <-time.After(time.Second):
				t.Fatal("first stdin command did not start")
			}
			assertActiveReplyCommand(t, store, conversation, first.replies.commands[0])
			if result := acceptStdinReply(t, router, conversation, 2, "reply-first"); result != AcceptRouted {
				t.Fatalf("active first reply ACL = %v, want routed", result)
			}
			if result := acceptStdinReply(t, router, conversation, 3, "reply-skipped"); result != AcceptIgnored {
				t.Fatalf("skipped second reply ACL while first is active = %v, want ignored", result)
			}
			firstRunner.finish <- struct{}{}
			select {
			case <-done:
			case <-time.After(time.Second):
				close(secondRunner.finish)
				t.Fatal("executor did not finish after skipped command")
			}
			if _, ok := store.lookup(conversation); ok {
				t.Fatal("skipped stdin command left an active endpoint")
			}
			result, err := router.Accept(&CommandInput{Text: "reply", ConversationID: conversation, MessageID: MessageID{Timestamp: "2"}, AllowedCommandIndexes: []int{3}})
			if err != nil || result != AcceptIgnored {
				t.Fatalf("reply matching skipped command ACL = %v, %v; want ignored", result, err)
			}
		})
	}
}

func TestExecutorInteractiveStdinPublishesAfterInitialIsOrdered(t *testing.T) {
	c := &stdinTestCmd{
		started: make(chan struct{}),
		read:    make(chan struct{}),
		lines:   make(chan string, 2),
	}
	var registry testThreadRegistry
	key := ConversationID{ChannelID: "C", RootTimestamp: "1"}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	rq := make(chan *CommandInput, 1)
	wq := make(chan *CommandOutput, 10)
	cfg := newTestCommandConfig(
		&testExecutionConfig{Keyword: "agent", Command: "agent"},
	)
	cfg.AllowInChain = false
	cfg.InteractiveStdin = true
	cfg.InputBodyMode = InputBodyStdin
	commandSet := testCommandSet([]*testCommandConfig{cfg}, func(*testExecutionConfig) CommandRunner {
		return singleCmdRunner{c}
	})
	input := &CommandInput{
		Text:                  "agent\ninitial",
		ConversationID:        ConversationID{ChannelID: "C", RootTimestamp: "1"},
		AllowedCommandIndexes: []int{0},
	}
	input.ResolvedInput = commandSet.ResolveInput(input.Text, input.AllowedCommandIndexes)
	rq <- input
	close(rq)
	done := make(chan struct{})
	go func() {
		testExecutorWithLifecycle(
			ctx,
			rq,
			wq,
			&registry,
		)
		close(done)
	}()
	select {
	case <-c.started:
	case <-ctx.Done():
		t.Fatal("not started")
	}
	endpoint := registry.lookup(key)
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
	if registry.lookup(key) != nil {
		t.Fatal("registration survived exit")
	}
}

type endpointProbeCmd struct {
	started chan struct{}
	finish  chan struct{}
}

func (*endpointProbeCmd) SetStdin(io.Reader)    {}
func (*endpointProbeCmd) SetStdout(io.Writer)   {}
func (*endpointProbeCmd) SetStderr(io.Writer)   {}
func (*endpointProbeCmd) Run(time.Duration) int { return 0 }
func (c *endpointProbeCmd) RunWithStdin(_ time.Duration, started func(io.WriteCloser)) int {
	reader, writer := io.Pipe()
	defer func() { _ = reader.Close() }()
	started(writer)
	close(c.started)
	<-c.finish
	_ = writer.Close()
	return 0
}

func TestExecutorDoesNotPublishLiveStdinWithoutInteractiveStdin(t *testing.T) {
	probe := &endpointProbeCmd{started: make(chan struct{}), finish: make(chan struct{})}
	var registry testThreadRegistry
	key := ConversationID{ChannelID: "C", RootTimestamp: "1"}
	rq := make(chan *CommandInput, 1)
	wq := make(chan *CommandOutput, 10)
	cfg := newTestCommandConfig(&testExecutionConfig{Keyword: "agent", Command: "agent"})
	commandSet := testCommandSet([]*testCommandConfig{cfg}, func(*testExecutionConfig) CommandRunner {
		return singleCmdRunner{probe}
	})
	input := &CommandInput{Text: "agent\ninitial", ConversationID: key, AllowedCommandIndexes: []int{0}}
	input.ResolvedInput = commandSet.ResolveInput(input.Text, input.AllowedCommandIndexes)
	rq <- input
	close(rq)
	done := make(chan struct{})
	go func() {
		testExecutorWithLifecycle(context.Background(), rq, wq, &registry)
		close(done)
	}()
	<-probe.started
	if endpoint := registry.lookup(key); endpoint != nil {
		t.Fatalf("unexpected endpoint = %v", endpoint)
	}
	close(probe.finish)
	<-done
}

func TestExecutorPublishesLifecycleOnlyForInteractiveChainCommand(t *testing.T) {
	stdinProbe := &endpointProbeCmd{started: make(chan struct{}), finish: make(chan struct{})}
	oneshotProbe := &endpointProbeCmd{started: make(chan struct{}), finish: make(chan struct{})}
	var registry testThreadRegistry
	key := ConversationID{ChannelID: "C", RootTimestamp: "1"}
	configs := []*testCommandConfig{
		newTestCommandConfig(&testExecutionConfig{Index: 0, Keyword: "stdin", Command: "stdin", AllowInChain: true, InteractiveStdin: true}),
		newTestCommandConfig(&testExecutionConfig{Index: 1, Keyword: "oneshot", Command: "oneshot", AllowInChain: true}),
	}
	commandSet := testCommandSet(configs, func(config *testExecutionConfig) CommandRunner {
		if config.Keyword == "stdin" {
			return singleCmdRunner{stdinProbe}
		}
		return singleCmdRunner{oneshotProbe}
	})
	input := &CommandInput{Text: "stdin ; oneshot", ConversationID: key, AllowedCommandIndexes: []int{0, 1}}
	input.ResolvedInput = commandSet.ResolveInput(input.Text, input.AllowedCommandIndexes)
	rq := make(chan *CommandInput, 1)
	rq <- input
	close(rq)
	done := make(chan struct{})
	go func() {
		testExecutorWithLifecycle(context.Background(), rq, make(chan *CommandOutput, 10), &registry)
		close(done)
	}()
	select {
	case <-stdinProbe.started:
	case <-time.After(time.Second):
		t.Fatal("stdin command did not start")
	}
	if registry.lookup(key) == nil {
		t.Fatal("interactive command did not publish its endpoint")
	}
	close(stdinProbe.finish)
	select {
	case <-oneshotProbe.started:
	case <-time.After(time.Second):
		t.Fatal("oneshot command did not start after stdin")
	}
	if registry.lookup(key) != nil {
		t.Fatal("oneshot command published a stdin endpoint")
	}
	close(oneshotProbe.finish)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("chain did not finish")
	}
}

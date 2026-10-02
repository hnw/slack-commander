package cmd

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func routerRoot(interaction string) *testCommandConfig {
	root := newTestCommandConfig(&testExecutionConfig{Keyword: "run", Command: "run"})
	switch interaction {
	case InteractionStdin:
		root.AllowInChain = false
		root.InteractiveStdin = true
		root.ThreadStdin = true
	case InteractionCommand:
		root.AllowInChain = false
		root.InputBodyMode = InputBodyArgument
		root.Replies = []*testCommandConfig{newTestCommandConfig(&testExecutionConfig{Index: 1, Keyword: "stop", Command: "stop"})}
	}
	return root
}

func TestConversationRouterCachesOnlyQueuedMatchedRoots(t *testing.T) {
	root := routerRoot(InteractionOneshot)
	queued := false
	c := newTestConversationRouter([]*testCommandConfig{root}, nil, func(*CommandInput) bool { return queued }, 2)
	input := &CommandInput{Text: "run", ConversationID: ConversationID{ChannelID: "C", RootTimestamp: "1"}, MessageID: MessageID{Timestamp: "1"}, AllowedCommandIndexes: []int{0}}
	if result, _ := c.Accept(input); result != AcceptQueueFull {
		t.Fatal("unexpected queue success")
	}
	if _, ok := c.routes.lookup(input.ConversationID); ok {
		t.Fatal("failed root enqueue was cached")
	}
	queued = true
	if result, _ := c.Accept(input); result != AcceptRouted {
		t.Fatal("root was not queued")
	}
	if got, ok := c.routes.lookup(input.ConversationID); !ok || got != c.commands.commands[0] {
		t.Fatalf("route = %v, %v", got, ok)
	}
}

func TestConversationRouterDoesNotCacheUnmatchedRoot(t *testing.T) {
	root := routerRoot(InteractionOneshot)
	queued := false
	router := newTestConversationRouter([]*testCommandConfig{root}, nil, func(*CommandInput) bool {
		queued = true
		return true
	}, 2)
	input := &CommandInput{
		Text:                  "unknown",
		ConversationID:        ConversationID{ChannelID: "C", RootTimestamp: "1"},
		MessageID:             MessageID{Timestamp: "1"},
		AllowedCommandIndexes: []int{0},
	}
	if router.commands.MatchSingle(input.Text, input.AllowedCommandIndexes) != nil {
		t.Fatal("test root unexpectedly matched")
	}
	if result, err := router.Accept(input); err != nil || result != AcceptRouted {
		t.Fatalf("Accept() = %v, %v; want routed", result, err)
	}
	if !queued {
		t.Fatal("unmatched root was not queued")
	}
	if _, ok := router.routes.lookup(input.ConversationID); ok {
		t.Fatal("unmatched root was cached")
	}
}

func TestConversationRouterResolvesEvictedRootWithOriginalCandidates(t *testing.T) {
	replySet := NewCommandSet([]*Command{NewCommand(CommandConfig{Index: 3, MatcherConfig: MatcherConfig{RawMatcherConfig: RawMatcherConfig{Keyword: "retry"}}, RunnerConfig: RunnerConfig{RawRunnerConfig: RawRunnerConfig{Command: "retry"}}}, nil, nil)})
	first := NewCommand(CommandConfig{Index: 1, MatcherConfig: MatcherConfig{RawMatcherConfig: RawMatcherConfig{Keyword: "run"}}, RunnerConfig: RunnerConfig{RawRunnerConfig: RawRunnerConfig{Command: "first"}}}, nil, nil)
	second := NewCommand(CommandConfig{Index: 2, MatcherConfig: MatcherConfig{RawMatcherConfig: RawMatcherConfig{Keyword: "run"}}, RunnerConfig: RunnerConfig{RawRunnerConfig: RawRunnerConfig{Command: "second"}}}, nil, replySet)
	commands := NewCommandSet([]*Command{first, second})
	var queued *CommandInput
	router := NewConversationRouterWithRootInputResolver(commands, func(ConversationID) (RootCommandInput, error) {
		return RootCommandInput{Text: "run", AllowedCommandIndexes: []int{2}}, nil
	}, newTestDispatcher(commands, func(input *CommandInput) bool { queued = input; return true }), 0)
	conversation := ConversationID{ChannelID: "C", RootTimestamp: "1"}
	if result, _ := router.Accept(&CommandInput{Text: "run", ConversationID: conversation, MessageID: MessageID{Timestamp: "1"}, AllowedCommandIndexes: []int{2}}); result != AcceptRouted {
		t.Fatal("AcceptRoot() failed")
	}
	if _, err := router.Accept(&CommandInput{Text: "retry", ConversationID: conversation, MessageID: MessageID{Timestamp: "2"}, AllowedCommandIndexes: []int{3}}); err != nil {
		t.Fatal(err)
	}
	if queued == nil || queued.CommandSet != replySet {
		t.Fatalf("thread reply route = %+v, want the ACL-selected root's reply set", queued)
	}
}

func TestConversationRouterPreservesNormalizedCommandRoot(t *testing.T) {
	root := routerRoot(InteractionCommand)
	var queued *CommandInput
	c := newTestConversationRouter([]*testCommandConfig{root}, nil, func(input *CommandInput) bool { queued = input; return true }, 2)
	if result, err := c.Accept(&CommandInput{Text: "run\nbody", ConversationID: ConversationID{ChannelID: "C", RootTimestamp: "1"}, MessageID: MessageID{Timestamp: "1"}, AllowedCommandIndexes: []int{0}}); err != nil || result != AcceptRouted {
		t.Fatalf("Accept() = %v, %v", result, err)
	}
	if queued == nil || queued.Text != "run\nbody" {
		t.Fatalf("queued=%+v", queued)
	}
}

func TestConversationRouterCachesResolverMatch(t *testing.T) {
	root := routerRoot(InteractionOneshot)
	lookups := 0
	c := newTestConversationRouter([]*testCommandConfig{root}, func(ConversationID) (RootCommandInput, error) {
		lookups++
		return RootCommandInput{Text: "run", AllowedCommandIndexes: []int{0}}, nil
	}, func(*CommandInput) bool { return true }, 2)
	input := &CommandInput{Text: "reply", ConversationID: ConversationID{ChannelID: "C", RootTimestamp: "1"}, AllowedCommandIndexes: []int{}}
	for range 2 {
		if _, err := c.Accept(input); err != nil {
			t.Fatal(err)
		}
	}
	if lookups != 1 {
		t.Fatalf("resolver calls = %d", lookups)
	}
}

func TestConversationRouterCachesResolverNonMatch(t *testing.T) {
	lookups := 0
	c := newTestConversationRouter(nil, func(ConversationID) (RootCommandInput, error) {
		lookups++
		return RootCommandInput{Text: "unknown", AllowedCommandIndexes: []int{0}}, nil
	}, func(*CommandInput) bool { return true }, 2)
	input := &CommandInput{Text: "reply", ConversationID: ConversationID{ChannelID: "C", RootTimestamp: "1"}, AllowedCommandIndexes: []int{}}
	if _, err := c.Accept(input); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Accept(input); err != nil {
		t.Fatal(err)
	}
	if lookups != 1 {
		t.Fatalf("resolver calls = %d", lookups)
	}
}

func TestConversationRouterEvictsLeastRecentlyUsedRoute(t *testing.T) {
	root := routerRoot(InteractionOneshot)
	lookups := 0
	c := newTestConversationRouter([]*testCommandConfig{root}, func(ConversationID) (RootCommandInput, error) {
		lookups++
		return RootCommandInput{Text: "run", AllowedCommandIndexes: []int{0}}, nil
	}, func(*CommandInput) bool { return true }, 2)
	for _, thread := range []string{"A", "B", "A", "C", "B"} {
		if _, err := c.Accept(&CommandInput{Text: "reply", ConversationID: ConversationID{ChannelID: "C", RootTimestamp: thread}, AllowedCommandIndexes: []int{}}); err != nil {
			t.Fatal(err)
		}
	}
	if lookups != 4 {
		t.Fatalf("resolver calls = %d, want 4", lookups)
	}
}

func TestConversationRouterDoesNotCacheResolverError(t *testing.T) {
	lookups := 0
	c := newTestConversationRouter(nil, func(ConversationID) (RootCommandInput, error) {
		lookups++
		return RootCommandInput{}, errors.New("failed")
	}, func(*CommandInput) bool { return true }, 2)
	input := &CommandInput{Text: "reply", ConversationID: ConversationID{ChannelID: "C", RootTimestamp: "1"}, AllowedCommandIndexes: []int{}}
	for range 2 {
		if _, err := c.Accept(input); err == nil {
			t.Fatal("missing resolver error")
		}
	}
	if lookups != 2 {
		t.Fatalf("resolver calls = %d", lookups)
	}
}

func TestConversationRouterRoutesCommandReplyAndReportsQueueFull(t *testing.T) {
	root := routerRoot(InteractionCommand)
	var queued *CommandInput
	c := newTestConversationRouter([]*testCommandConfig{root}, func(ConversationID) (RootCommandInput, error) {
		return RootCommandInput{Text: "run", AllowedCommandIndexes: []int{0}}, nil
	}, func(input *CommandInput) bool { queued = input; return true }, 2)
	result, err := c.Accept(&CommandInput{Text: "stop && stop\nbody", ConversationID: ConversationID{ChannelID: "C", RootTimestamp: "1"}, AllowedCommandIndexes: []int{1}})
	if err != nil {
		t.Fatal(err)
	}
	if result != AcceptRouted {
		t.Fatalf("result=%v", result)
	}
	if queued == nil {
		t.Fatal("reply was not queued")
	}
	if queued.CommandSet != c.commands.commands[0].replies || queued.Text != "stop && stop\nbody" {
		t.Fatalf("queued=%+v", queued)
	}
	c.dispatcher = newTestDispatcher(c.commands, func(*CommandInput) bool { return false })
	result, err = c.Accept(&CommandInput{Text: "stop", ConversationID: ConversationID{ChannelID: "C", RootTimestamp: "1"}, AllowedCommandIndexes: []int{1}})
	if err != nil || result != AcceptQueueFull {
		t.Fatalf("result=%v err=%v", result, err)
	}
}

func TestConversationRouterQueuesMalformedExplicitReplyForExecutorError(t *testing.T) {
	root := routerRoot(InteractionCommand)
	root.Replies[0].Keyword = "stop *"
	var queued *CommandInput
	router := newTestConversationRouter([]*testCommandConfig{root}, func(ConversationID) (RootCommandInput, error) {
		return RootCommandInput{Text: "run", AllowedCommandIndexes: []int{0}}, nil
	}, func(input *CommandInput) bool {
		queued = input
		return true
	}, 1)
	result, err := router.Accept(&CommandInput{
		Text:                  `stop "unbalanced`,
		ConversationID:        ConversationID{ChannelID: "C", RootTimestamp: "1"},
		MessageID:             MessageID{Timestamp: "2"},
		AllowedCommandIndexes: []int{1},
	})
	if err != nil || result != AcceptRouted || queued == nil {
		t.Fatalf("Accept() = %v, %v; queued = %+v", result, err, queued)
	}
	outputs := make(chan *CommandOutput, 10)
	NewExecutor(router.commands, outputs).Execute(context.Background(), queued, nil)
	var parseError bool
	for range len(outputs) {
		output := <-outputs
		if output.IsErrOut && strings.Contains(output.Text, "Parse error") {
			parseError = true
		}
	}
	if !parseError {
		t.Fatal("Executor did not report the explicit reply parse error")
	}
}

func TestConversationRouterIgnoresOneshotReply(t *testing.T) {
	root := routerRoot(InteractionOneshot)
	c := newTestConversationRouter([]*testCommandConfig{root}, func(ConversationID) (RootCommandInput, error) {
		return RootCommandInput{Text: "run", AllowedCommandIndexes: []int{0}}, nil
	}, func(*CommandInput) bool { t.Fatal("oneshot reply queued"); return false }, 2)
	result, err := c.Accept(&CommandInput{Text: "reply", ConversationID: ConversationID{ChannelID: "C", RootTimestamp: "1"}, AllowedCommandIndexes: []int{}})
	if err != nil || result != AcceptIgnored {
		t.Fatalf("result=%v err=%v", result, err)
	}
}

func TestConversationRouterDropsStdinReplyWithoutEndpoint(t *testing.T) {
	root := routerRoot(InteractionStdin)
	queued := false
	c := newTestConversationRouter([]*testCommandConfig{root}, func(ConversationID) (RootCommandInput, error) {
		return RootCommandInput{Text: "run", AllowedCommandIndexes: []int{0}}, nil
	}, func(*CommandInput) bool { queued = true; return true }, 2)
	result, err := c.Accept(&CommandInput{Text: "reply", ConversationID: ConversationID{ChannelID: "C", RootTimestamp: "1"}, AllowedCommandIndexes: []int{1000}})
	if err != nil || result != AcceptIgnored || queued {
		t.Fatalf("result=%v err=%v queued=%v", result, err, queued)
	}
}

func TestConversationRouterRoutesRawStdinReply(t *testing.T) {
	root := routerRoot(InteractionStdin)
	ctx := ConversationID{ChannelID: "C", RootTimestamp: "1"}
	store := &StdinStore{}
	commands := testCommandSet([]*testCommandConfig{root}, nil)
	commands.commands[0].replies.commands[0].runner = NewStdinReplyRunner(store)
	c := NewConversationRouterWithRootInputResolver(commands, func(ConversationID) (RootCommandInput, error) {
		return RootCommandInput{Text: "run", AllowedCommandIndexes: []int{0}}, nil
	}, newTestDispatcher(commands, func(*CommandInput) bool { t.Fatal("stdin reply queued"); return false }), 2)
	reader, writer := io.Pipe()
	endpoint := NewInteractiveStdin(writer, "", func(error) {})
	t.Cleanup(func() {
		endpoint.Close()
		_ = reader.Close()
	})
	store.Lifecycle(ctx).StdinReady(endpoint)
	for _, body := range []string{"<@BOT> “raw” &amp;", "hello world", "yes && no", "yes || no", "foo ; bar", `unbalanced "quote`, "first line\nsecond line"} {
		result, err := c.Accept(&CommandInput{Text: body, ConversationID: ctx, AllowedCommandIndexes: []int{1000}})
		if err != nil || result != AcceptRouted {
			t.Fatalf("body %q: result=%v err=%v", body, result, err)
		}
		want := body + "\n"
		read := make(chan string, 1)
		go func() {
			buffer := make([]byte, len(want))
			_, _ = io.ReadFull(reader, buffer)
			read <- string(buffer)
		}()
		select {
		case got := <-read:
			if got != want {
				t.Fatalf("body %q delivered %q, want %q", body, got, want)
			}
		case <-time.After(time.Second):
			t.Fatalf("stdin reply %q was not routed", body)
		}
	}
}

func TestMatchReplyKeepsSpecificBeforeWildcard(t *testing.T) {
	specific := NewCommand(CommandConfig{Index: 1, MatcherConfig: MatcherConfig{RawMatcherConfig: RawMatcherConfig{Keyword: "retry *"}}, RunnerConfig: RunnerConfig{RawRunnerConfig: RawRunnerConfig{Command: "specific *"}}}, nil, nil)
	wildcard := NewCommand(CommandConfig{Index: 2, MatcherConfig: MatcherConfig{RawMatcherConfig: RawMatcherConfig{Keyword: "*"}}, RunnerConfig: RunnerConfig{RawRunnerConfig: RawRunnerConfig{Command: "fallback *"}}}, nil, nil)
	set := NewCommandSet([]*Command{specific, wildcard})
	got, args := set.MatchReply("retry value", []int{1, 2})
	if got != specific || len(args) != 2 || args[0] != "specific" || args[1] != "value" {
		t.Fatalf("MatchReply() = (%v, %v), want specific command before wildcard", got, args)
	}
}

func TestRouterDirectDispatchUsesCommandRunnerAndKeepsWholeBody(t *testing.T) {
	store := &StdinStore{}
	conversation := ConversationID{ChannelID: "C", RootTimestamp: "1"}
	stdinReply := NewCommand(CommandConfig{
		Index:          1,
		MatcherConfig:  MatcherConfig{RawMatcherConfig: RawMatcherConfig{Keyword: "*"}},
		ParserConfig:   ParserConfig{InputBodyMode: InputBodyRawStdin},
		RunnerConfig:   RunnerConfig{RawRunnerConfig: RawRunnerConfig{Runner: RunnerStdinReply, Command: "stdin-reply"}},
		DispatchPolicy: DispatchDirect,
	}, NewStdinReplyRunner(store), nil)
	root := NewCommand(CommandConfig{Index: 0, MatcherConfig: MatcherConfig{RawMatcherConfig: RawMatcherConfig{Keyword: "run"}}, RunnerConfig: RunnerConfig{RawRunnerConfig: RawRunnerConfig{Command: "run"}}}, nil, NewCommandSet([]*Command{stdinReply}))
	queued := 0
	commands := NewCommandSet([]*Command{root})
	router := NewConversationRouterWithRootInputResolver(commands, nil, newTestDispatcher(commands, func(*CommandInput) bool {
		queued++
		return true
	}), 1)
	if result, err := router.Accept(&CommandInput{Text: "run", ConversationID: conversation, MessageID: MessageID{Timestamp: "1"}, AllowedCommandIndexes: []int{0}}); err != nil || result != AcceptRouted {
		t.Fatalf("root command = %v, %v", result, err)
	}
	reader, writer := io.Pipe()
	endpoint := NewInteractiveStdin(writer, "", func(error) {})
	t.Cleanup(func() {
		endpoint.Close()
		_ = reader.Close()
	})
	store.Lifecycle(conversation).StdinReady(endpoint)
	body := "yes && no\nunbalanced \"quote"
	input := &CommandInput{Text: body, ConversationID: conversation, MessageID: MessageID{Timestamp: "2"}, AllowedCommandIndexes: []int{1}}
	if result, err := router.Accept(input); err != nil || result != AcceptRouted || queued != 1 {
		t.Fatalf("stdin reply = %v, %v; queue calls=%d", result, err, queued)
	}
	want := body + "\n"
	read := make(chan string, 1)
	go func() {
		got := make([]byte, len(want))
		_, _ = io.ReadFull(reader, got)
		read <- string(got)
	}()
	select {
	case got := <-read:
		if got != want {
			t.Fatalf("stdin reply body = %q; want %q", got, want)
		}
	case <-time.After(time.Second):
		t.Fatal("stdin reply body was not delivered")
	}
}

func TestDispatcherKeepsRootAndReplyQueueUnavailableResultsDistinct(t *testing.T) {
	reply := NewCommand(CommandConfig{
		Index:         1,
		MatcherConfig: MatcherConfig{RawMatcherConfig: RawMatcherConfig{Keyword: "stop"}},
		RunnerConfig:  RunnerConfig{RawRunnerConfig: RawRunnerConfig{Command: "stop"}},
	}, nil, nil)
	root := NewCommand(CommandConfig{
		Index:         0,
		MatcherConfig: MatcherConfig{RawMatcherConfig: RawMatcherConfig{Keyword: "run"}},
		RunnerConfig:  RunnerConfig{RawRunnerConfig: RawRunnerConfig{Command: "run"}},
	}, nil, NewCommandSet([]*Command{reply}))
	commands := NewCommandSet([]*Command{root})
	dispatcher := newTestDispatcher(commands, nil)
	conversation := ConversationID{ChannelID: "C", RootTimestamp: "1"}

	rootRouter := NewConversationRouterWithRootInputResolver(commands, nil, dispatcher, 1)
	if result, _ := rootRouter.Accept(&CommandInput{Text: "run", ConversationID: conversation, MessageID: MessageID{Timestamp: "1"}, AllowedCommandIndexes: []int{0}}); result != AcceptQueueFull {
		t.Fatalf("root Accept() = %v, want queue full", result)
	}

	replyRouter := NewConversationRouterWithRootInputResolver(commands, func(ConversationID) (RootCommandInput, error) {
		return RootCommandInput{Text: "run", AllowedCommandIndexes: []int{0}}, nil
	}, dispatcher, 1)
	if result, err := replyRouter.Accept(&CommandInput{Text: "stop", ConversationID: conversation, MessageID: MessageID{Timestamp: "2"}, AllowedCommandIndexes: []int{1}}); err != nil || result != AcceptIgnored {
		t.Fatalf("reply Accept() = %v, %v; want ignored", result, err)
	}
}

type dispatcherExitCodeRunner int

func (r dispatcherExitCodeRunner) CommandContext(context.Context, string, ...string) Cmd {
	return &fakeCmd{exitCode: int(r)}
}

func TestDispatcherIgnoresFailedDirectReply(t *testing.T) {
	reply := NewCommand(CommandConfig{
		Index:          1,
		MatcherConfig:  MatcherConfig{RawMatcherConfig: RawMatcherConfig{Keyword: "stop"}},
		RunnerConfig:   RunnerConfig{RawRunnerConfig: RawRunnerConfig{Command: "stop"}},
		DispatchPolicy: DispatchDirect,
	}, dispatcherExitCodeRunner(1), nil)
	root := NewCommand(CommandConfig{
		Index:         0,
		MatcherConfig: MatcherConfig{RawMatcherConfig: RawMatcherConfig{Keyword: "run"}},
		RunnerConfig:  RunnerConfig{RawRunnerConfig: RawRunnerConfig{Command: "run"}},
	}, nil, NewCommandSet([]*Command{reply}))
	queued := false
	commands := NewCommandSet([]*Command{root})
	router := NewConversationRouterWithRootInputResolver(commands, func(ConversationID) (RootCommandInput, error) {
		return RootCommandInput{Text: "run", AllowedCommandIndexes: []int{0}}, nil
	}, newTestDispatcher(commands, func(*CommandInput) bool { queued = true; return true }), 1)
	result, err := router.Accept(&CommandInput{Text: "stop", ConversationID: ConversationID{ChannelID: "C", RootTimestamp: "1"}, MessageID: MessageID{Timestamp: "2"}, AllowedCommandIndexes: []int{1}})
	if err != nil || result != AcceptIgnored || queued {
		t.Fatalf("direct reply = %v, %v; queued=%v, want ignored without queueing", result, err, queued)
	}
}

func TestHTTPReplyUsesExecutorOutputPipeline(t *testing.T) {
	requestStarted := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requestStarted <- struct{}{}
		_, _ = io.WriteString(w, "response")
	}))
	defer server.Close()
	outputs := make(chan *CommandOutput, 20)
	queueCalls := 0
	reply := NewCommand(CommandConfig{
		Index:         1,
		MatcherConfig: MatcherConfig{RawMatcherConfig: RawMatcherConfig{Keyword: "lookup *"}},
		RunnerConfig:  RunnerConfig{RawRunnerConfig: RawRunnerConfig{Runner: RunnerHTTP, Method: "GET", URL: server.URL + "/?q=*"}},
		ReplyConfig:   "reply",
	}, NewHTTPRunner(RunnerConfig{RawRunnerConfig: RawRunnerConfig{Runner: RunnerHTTP, Method: "GET", URL: server.URL + "/?q=*"}}), nil)
	root := NewCommand(CommandConfig{Index: 0, MatcherConfig: MatcherConfig{RawMatcherConfig: RawMatcherConfig{Keyword: "run"}}, RunnerConfig: RunnerConfig{RawRunnerConfig: RawRunnerConfig{Command: "run"}}}, nil, NewCommandSet([]*Command{reply}))
	commands := NewCommandSet([]*Command{root})
	dispatcher := NewCommandDispatcher(context.Background(), NewExecutor(commands, outputs), &StdinStore{}, &ConversationLocks{}, func(*CommandInput) bool {
		queueCalls++
		return true
	})
	router := NewConversationRouterWithRootInputResolver(commands, nil, dispatcher, 1)
	conversation := ConversationID{ChannelID: "C", RootTimestamp: "1"}
	if result, err := router.Accept(&CommandInput{Text: "run", ConversationID: conversation, MessageID: MessageID{Timestamp: "1"}, AllowedCommandIndexes: []int{0}}); err != nil || result != AcceptRouted {
		t.Fatalf("root command = %v, %v", result, err)
	}
	queueCalls = 0
	input := &CommandInput{Text: "lookup value", ConversationID: conversation, MessageID: MessageID{Timestamp: "2"}, AllowedCommandIndexes: []int{1}}
	if result, err := router.Accept(input); err != nil || result != AcceptRouted || queueCalls != 0 || input.CommandSet != root.replies {
		t.Fatalf("HTTP reply = %v, %v; queue calls=%d commandSet=%p", result, err, queueCalls, input.CommandSet)
	}
	dispatcher.Close()
	dispatcher.Wait()
	if len(requestStarted) == 0 {
		t.Fatal("HTTP reply request did not start")
	}
	assertHTTPReplyOutputs(t, outputs, conversation, "2")
}

func assertHTTPReplyOutputs(t *testing.T, outputs chan *CommandOutput, conversation ConversationID, messageTimestamp string) {
	t.Helper()
	var body strings.Builder
	spawned, finished := false, false
	for len(outputs) > 0 {
		output := <-outputs
		if output.ConversationID != conversation || output.MessageID.Timestamp != messageTimestamp {
			t.Fatalf("output context = %+v/%+v", output.ConversationID, output.MessageID)
		}
		if output.Text != "" && output.ReplyConfig != "reply" {
			t.Fatalf("ReplyConfig = %v, want reply config", output.ReplyConfig)
		}
		spawned = spawned || output.Spawned
		finished = finished || output.Finished && output.ExitCode == 0
		body.WriteString(output.Text)
	}
	if !spawned || !finished || body.String() != "response" {
		t.Fatalf("output spawned=%v finished=%v body=%q", spawned, finished, body.String())
	}
}

func TestHTTPRootBypassesFullQueueAndCachesOwnership(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "root response")
	}))
	defer server.Close()
	outputs := make(chan *CommandOutput, 20)
	reply := NewCommand(CommandConfig{
		Index:         1,
		MatcherConfig: MatcherConfig{RawMatcherConfig: RawMatcherConfig{Keyword: "stop"}},
		RunnerConfig:  RunnerConfig{RawRunnerConfig: RawRunnerConfig{Command: "stop"}},
	}, nil, nil)
	httpConfig := RunnerConfig{RawRunnerConfig: RawRunnerConfig{Runner: RunnerHTTP, Method: "GET", URL: server.URL}}
	root := NewCommand(CommandConfig{
		Index:         0,
		MatcherConfig: MatcherConfig{RawMatcherConfig: RawMatcherConfig{Keyword: "lookup"}},
		RunnerConfig:  httpConfig,
	}, NewHTTPRunner(httpConfig), NewCommandSet([]*Command{reply}))
	commands := NewCommandSet([]*Command{root})
	queueCalls := 0
	dispatcher := NewCommandDispatcher(context.Background(), NewExecutor(commands, outputs), &StdinStore{}, &ConversationLocks{}, func(*CommandInput) bool {
		queueCalls++
		return false
	})
	router := NewConversationRouterWithRootInputResolver(commands, nil, dispatcher, 1)
	defer func() { dispatcher.Close(); dispatcher.Wait() }()
	conversation := ConversationID{ChannelID: "C", RootTimestamp: "1"}
	if result, err := router.Accept(&CommandInput{Text: "lookup", ConversationID: conversation, MessageID: MessageID{Timestamp: "1"}, AllowedCommandIndexes: []int{0}}); err != nil || result != AcceptRouted {
		t.Fatalf("HTTP root Accept() = %v, %v; want routed despite full queue", result, err)
	}
	replyInput := &CommandInput{Text: "stop", ConversationID: conversation, MessageID: MessageID{Timestamp: "2"}, AllowedCommandIndexes: []int{1}}
	if result, err := router.Accept(replyInput); err != nil || result != AcceptQueueFull || replyInput.CommandSet != root.replies {
		t.Fatalf("cached root reply = %v, %v; commandSet=%p, want queue full and cached reply set", result, err, replyInput.CommandSet)
	}
	if queueCalls != 1 {
		t.Fatalf("queue calls = %d, want only the queued reply", queueCalls)
	}
	for len(outputs) > 0 {
		<-outputs
	}
}

func TestStdinReplyCommandSucceedsWhenEndpointRejectsInput(t *testing.T) {
	conversation := ConversationID{ChannelID: "C", RootTimestamp: "1"}
	endpoint := newInteractiveStdinSession("", 0, nil)
	store := &StdinStore{}
	store.register(conversation, endpoint)
	runner := NewStdinReplyRunner(store)
	if err := endpoint.TrySend("buffered"); err != nil {
		t.Fatal(err)
	}
	if err := endpoint.TrySend("probe"); err != ErrInteractiveStdinBusy {
		t.Fatalf("second TrySend() error = %v, want full-buffer error", err)
	}
	run := func() int {
		command := runner.CommandContext(context.Background(), "stdin-reply", conversation.ChannelID, conversation.RootTimestamp)
		command.SetStdin(strings.NewReader("reply"))
		return command.Run(0)
	}
	if got := run(); got != 0 {
		t.Fatalf("Run() with full endpoint = %d, want success", got)
	}
	endpoint.Close()
	if err := endpoint.TrySend("probe"); err != ErrInteractiveStdinClosed {
		t.Fatalf("TrySend() after close error = %v, want closed error", err)
	}
	if got := run(); got != 0 {
		t.Fatalf("Run() with closed endpoint = %d, want success", got)
	}
}

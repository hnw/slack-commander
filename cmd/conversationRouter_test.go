package cmd

import (
	"context"
	"errors"
	"io"
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
	replySet := NewCommandSet([]*Command{NewCommand(CommandConfig{Index: 3, MatcherConfig: MatcherConfig{Keyword: "retry"}, RunnerConfig: RunnerConfig{Command: "retry"}}, nil, nil)})
	first := NewCommand(CommandConfig{Index: 1, MatcherConfig: MatcherConfig{Keyword: "run"}, RunnerConfig: RunnerConfig{Command: "first"}}, nil, nil)
	second := NewCommand(CommandConfig{Index: 2, MatcherConfig: MatcherConfig{Keyword: "run"}, RunnerConfig: RunnerConfig{Command: "second"}}, nil, replySet)
	commands := NewCommandSet([]*Command{first, second})
	var queued *CommandInput
	router := NewConversationRouterWithRootInputResolver(commands, func(ConversationID) (RootCommandInput, error) {
		return RootCommandInput{Text: "run", AllowedCommandIndexes: []int{2}}, nil
	}, func(input *CommandInput) bool { queued = input; return true }, 0, &StdinStore{})
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
	c.enqueue = func(*CommandInput) bool { return false }
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
	c := newTestConversationRouter([]*testCommandConfig{root}, func(ConversationID) (RootCommandInput, error) {
		return RootCommandInput{Text: "run", AllowedCommandIndexes: []int{0}}, nil
	}, func(*CommandInput) bool { t.Fatal("stdin reply queued"); return false }, 2)
	reader, writer := io.Pipe()
	endpoint := NewInteractiveStdin(writer, "", func(error) {})
	store := &StdinStore{}
	c.stdin = store
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
	endpoint.Close()
	_ = reader.Close()
}

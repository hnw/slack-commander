package cmd

import (
	"bufio"
	"errors"
	"io"
	"sync"
	"testing"
	"time"
)

func coordinatorRoot(interaction string) *testCommandConfig {
	root := newTestCommandConfig(&testExecutionConfig{Keyword: "run", Command: "run"})
	switch interaction {
	case InteractionStdin:
		root.AllowInChain = false
		root.InteractiveStdin = true
		root.ThreadReplyMode = ThreadReplyStdin
	case InteractionCommand:
		root.AllowInChain = false
		root.InputBodyMode = InputBodyArgument
		root.ThreadReplyMode = ThreadReplyCommand
	}
	root.Replies = []*testCommandConfig{newTestCommandConfig(&testExecutionConfig{Keyword: "stop", Command: "stop"})}
	return root
}

func TestConversationCoordinatorCachesOnlyQueuedMatchedRoots(t *testing.T) {
	root := coordinatorRoot(InteractionOneshot)
	queued := false
	c := newTestConversationCoordinator([]*testCommandConfig{root}, nil, func(*CommandInput) bool { return queued }, 2)
	input := &CommandInput{Text: "run", ConversationID: ConversationID{ChannelID: "C", RootTimestamp: "1"}}
	if c.AcceptRoot(input) {
		t.Fatal("unexpected queue success")
	}
	if _, ok := c.routes.lookup(input.ConversationID); ok {
		t.Fatal("failed root enqueue was cached")
	}
	queued = true
	if !c.AcceptRoot(input) {
		t.Fatal("root was not queued")
	}
	if got, ok := c.routes.lookup(input.ConversationID); !ok || got != c.commands.commands[0] {
		t.Fatalf("route = %v, %v", got, ok)
	}
}

func TestConversationCoordinatorResolvesEvictedRootWithOriginalCandidates(t *testing.T) {
	replySet := NewCommandSet([]*Command{NewCommand(CommandConfig{Index: 3}, nil, nil)})
	first := NewCommand(CommandConfig{Index: 1, MatcherConfig: MatcherConfig{Keyword: "run"}, RunnerConfig: RunnerConfig{Command: "first"}, ThreadReplyMode: ThreadReplyIgnore}, nil, nil)
	second := NewCommand(CommandConfig{Index: 2, MatcherConfig: MatcherConfig{Keyword: "run"}, RunnerConfig: RunnerConfig{Command: "second"}, ThreadReplyMode: ThreadReplyCommand}, nil, replySet)
	commands := NewCommandSet([]*Command{first, second})
	var queued *CommandInput
	coordinator := NewConversationCoordinatorWithRootInputResolver(commands, func(ConversationID) (RootCommandInput, error) {
		return RootCommandInput{Text: "run", AllowedCommandIndexes: []int{2}}, nil
	}, func(input *CommandInput) bool { queued = input; return true }, 0)
	conversation := ConversationID{ChannelID: "C", RootTimestamp: "1"}
	if !coordinator.AcceptRoot(&CommandInput{Text: "run", ConversationID: conversation, AllowedCommandIndexes: []int{2}}) {
		t.Fatal("AcceptRoot() failed")
	}
	if _, err := coordinator.AcceptThreadReply(&CommandInput{Text: "retry", ConversationID: conversation}); err != nil {
		t.Fatal(err)
	}
	if queued == nil || queued.CommandSet != replySet {
		t.Fatalf("thread reply route = %+v, want the ACL-selected root's reply set", queued)
	}
}

func TestConversationCoordinatorPreservesNormalizedCommandRoot(t *testing.T) {
	root := coordinatorRoot(InteractionCommand)
	var queued *CommandInput
	c := newTestConversationCoordinator([]*testCommandConfig{root}, nil, func(input *CommandInput) bool { queued = input; return true }, 2)
	c.AcceptRoot(&CommandInput{Text: "run\nbody", ConversationID: ConversationID{ChannelID: "C", RootTimestamp: "1"}})
	if queued == nil || queued.Text != "run\nbody" {
		t.Fatalf("queued=%+v", queued)
	}
}

func TestConversationCoordinatorCachesResolverMatch(t *testing.T) {
	root := coordinatorRoot(InteractionOneshot)
	lookups := 0
	c := newTestConversationCoordinator([]*testCommandConfig{root}, func(ConversationID) (RootCommandInput, error) { lookups++; return RootCommandInput{Text: "run"}, nil }, func(*CommandInput) bool { return true }, 2)
	input := &CommandInput{Text: "reply", ConversationID: ConversationID{ChannelID: "C", RootTimestamp: "1"}}
	for range 2 {
		if _, err := c.AcceptThreadReply(input); err != nil {
			t.Fatal(err)
		}
	}
	if lookups != 1 {
		t.Fatalf("resolver calls = %d", lookups)
	}
}

func TestConversationCoordinatorCachesResolverNonMatch(t *testing.T) {
	lookups := 0
	c := newTestConversationCoordinator(nil, func(ConversationID) (RootCommandInput, error) {
		lookups++
		return RootCommandInput{Text: "unknown"}, nil
	}, func(*CommandInput) bool { return true }, 2)
	input := &CommandInput{Text: "reply", ConversationID: ConversationID{ChannelID: "C", RootTimestamp: "1"}}
	if _, err := c.AcceptThreadReply(input); err != nil {
		t.Fatal(err)
	}
	if _, err := c.AcceptThreadReply(input); err != nil {
		t.Fatal(err)
	}
	if lookups != 1 {
		t.Fatalf("resolver calls = %d", lookups)
	}
}

func TestConversationCoordinatorEvictsLeastRecentlyUsedRoute(t *testing.T) {
	root := coordinatorRoot(InteractionOneshot)
	lookups := 0
	c := newTestConversationCoordinator([]*testCommandConfig{root}, func(ConversationID) (RootCommandInput, error) {
		lookups++
		return RootCommandInput{Text: "run"}, nil
	}, func(*CommandInput) bool { return true }, 2)
	for _, thread := range []string{"A", "B", "A", "C", "B"} {
		if _, err := c.AcceptThreadReply(&CommandInput{Text: "reply", ConversationID: ConversationID{ChannelID: "C", RootTimestamp: thread}}); err != nil {
			t.Fatal(err)
		}
	}
	if lookups != 4 {
		t.Fatalf("resolver calls = %d, want 4", lookups)
	}
}

func TestConversationCoordinatorDoesNotCacheResolverError(t *testing.T) {
	lookups := 0
	c := newTestConversationCoordinator(nil, func(ConversationID) (RootCommandInput, error) {
		lookups++
		return RootCommandInput{}, errors.New("failed")
	}, func(*CommandInput) bool { return true }, 2)
	input := &CommandInput{Text: "reply", ConversationID: ConversationID{ChannelID: "C", RootTimestamp: "1"}}
	for range 2 {
		if _, err := c.AcceptThreadReply(input); err == nil {
			t.Fatal("missing resolver error")
		}
	}
	if lookups != 2 {
		t.Fatalf("resolver calls = %d", lookups)
	}
}

func TestConversationCoordinatorRoutesCommandReplyAndReportsQueueFull(t *testing.T) {
	root := coordinatorRoot(InteractionCommand)
	var queued *CommandInput
	c := newTestConversationCoordinator([]*testCommandConfig{root}, func(ConversationID) (RootCommandInput, error) { return RootCommandInput{Text: "run"}, nil }, func(input *CommandInput) bool { queued = input; return true }, 2)
	result, err := c.AcceptThreadReply(&CommandInput{Text: "reply\nbody", ConversationID: ConversationID{ChannelID: "C", RootTimestamp: "1"}})
	if err != nil {
		t.Fatal(err)
	}
	if result != ThreadReplyRouted {
		t.Fatalf("result=%v", result)
	}
	if queued == nil {
		t.Fatal("reply was not queued")
	}
	if queued.CommandSet != c.commands.commands[0].replies || queued.Text != "reply\nbody" {
		t.Fatalf("queued=%+v", queued)
	}
	c.enqueue = func(*CommandInput) bool { return false }
	result, err = c.AcceptThreadReply(&CommandInput{Text: "reply", ConversationID: ConversationID{ChannelID: "C", RootTimestamp: "1"}})
	if err != nil || result != ThreadReplyQueueFull {
		t.Fatalf("result=%v err=%v", result, err)
	}
}

func TestConversationCoordinatorIgnoresOneshotReply(t *testing.T) {
	root := coordinatorRoot(InteractionOneshot)
	c := newTestConversationCoordinator([]*testCommandConfig{root}, func(ConversationID) (RootCommandInput, error) { return RootCommandInput{Text: "run"}, nil }, func(*CommandInput) bool { t.Fatal("oneshot reply queued"); return false }, 2)
	result, err := c.AcceptThreadReply(&CommandInput{Text: "reply", ConversationID: ConversationID{ChannelID: "C", RootTimestamp: "1"}})
	if err != nil || result != ThreadReplyIgnored {
		t.Fatalf("result=%v err=%v", result, err)
	}
}

func TestConversationCoordinatorSerializesSameContext(t *testing.T) {
	c := newTestConversationCoordinator(nil, nil, nil, 1)
	ctx := ConversationID{ChannelID: "C", RootTimestamp: "1"}
	started := make(chan struct{}, 2)
	release := make(chan struct{}, 2)
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() { defer wg.Done(); c.RunSerialized(ctx, func() { started <- struct{}{}; <-release }) }()
	}
	<-started
	select {
	case <-started:
		t.Fatal("same context ran concurrently")
	case <-time.After(50 * time.Millisecond):
	}
	release <- struct{}{}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("second same-context command did not start")
	}
	release <- struct{}{}
	wg.Wait()
}

func TestConversationCoordinatorRunsDifferentContextsConcurrently(t *testing.T) {
	c := newTestConversationCoordinator(nil, nil, nil, 1)
	started := make(chan struct{}, 2)
	release := make(chan struct{}, 2)
	var wg sync.WaitGroup
	for _, thread := range []string{"1", "2"} {
		wg.Add(1)
		go func(thread string) {
			defer wg.Done()
			c.RunSerialized(ConversationID{ChannelID: "C", RootTimestamp: thread}, func() { started <- struct{}{}; <-release })
		}(thread)
	}
	for range 2 {
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("different contexts did not run concurrently")
		}
	}
	release <- struct{}{}
	release <- struct{}{}
	wg.Wait()
}

func TestConversationCoordinatorKeepsNewestEndpoint(t *testing.T) {
	root := coordinatorRoot(InteractionStdin)
	c := newTestConversationCoordinator([]*testCommandConfig{root}, nil, func(*CommandInput) bool { t.Fatal("stdin reply queued"); return false }, 2)
	ctx := ConversationID{ChannelID: "C", RootTimestamp: "1"}
	c.routes.store(ctx, c.commands.commands[0])
	_, oldWriter := io.Pipe()
	_, newWriter := io.Pipe()
	old := NewInteractiveStdin(oldWriter, "", func(error) {})
	newEndpoint := NewInteractiveStdin(newWriter, "", func(error) {})
	lifecycle := c.Lifecycle(ctx)
	lifecycle.StdinReady(old)
	lifecycle.StdinReady(newEndpoint)
	lifecycle.StdinClosed(old)
	if got := c.inputs.lookup(ctx); got != newEndpoint {
		t.Fatal("old endpoint close removed new endpoint")
	}
	lifecycle.StdinClosed(newEndpoint)
	old.Close()
	newEndpoint.Close()
	_ = oldWriter.Close()
	_ = newWriter.Close()
}

func TestConversationCoordinatorDropsStdinReplyWithoutEndpoint(t *testing.T) {
	root := coordinatorRoot(InteractionStdin)
	queued := false
	c := newTestConversationCoordinator([]*testCommandConfig{root}, func(ConversationID) (RootCommandInput, error) { return RootCommandInput{Text: "run"}, nil }, func(*CommandInput) bool { queued = true; return true }, 2)
	result, err := c.AcceptThreadReply(&CommandInput{Text: "reply", ConversationID: ConversationID{ChannelID: "C", RootTimestamp: "1"}})
	if err != nil || result != ThreadReplyIgnored || queued {
		t.Fatalf("result=%v err=%v queued=%v", result, err, queued)
	}
}

func TestConversationCoordinatorRoutesRawStdinReply(t *testing.T) {
	root := coordinatorRoot(InteractionStdin)
	ctx := ConversationID{ChannelID: "C", RootTimestamp: "1"}
	c := newTestConversationCoordinator([]*testCommandConfig{root}, func(ConversationID) (RootCommandInput, error) { return RootCommandInput{Text: "run"}, nil }, func(*CommandInput) bool { t.Fatal("stdin reply queued"); return false }, 2)
	reader, writer := io.Pipe()
	endpoint := NewInteractiveStdin(writer, "", func(error) {})
	c.Lifecycle(ctx).StdinReady(endpoint)
	result, err := c.AcceptThreadReply(&CommandInput{Text: "<@BOT> “raw” &amp;", ConversationID: ctx})
	if err != nil || result != ThreadReplyRouted {
		t.Fatalf("result=%v err=%v", result, err)
	}
	line := make(chan string, 1)
	go func() { value, _ := bufio.NewReader(reader).ReadString('\n'); line <- value }()
	select {
	case got := <-line:
		if got != "<@BOT> “raw” &amp;\n" {
			t.Fatalf("stdin=%q", got)
		}
	case <-time.After(time.Second):
		t.Fatal("stdin reply was not routed")
	}
	endpoint.Close()
	_ = reader.Close()
}

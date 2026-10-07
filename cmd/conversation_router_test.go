package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

func routerRoot(interaction string) *testCommandConfig {
	root := newTestCommandConfig(&testExecutionConfig{Keyword: "run", Command: "run"})
	switch interaction {
	case InteractionStdin:
		root.AllowInChain = true
		root.InteractiveStdin = true
		root.ThreadStdin = true
	case InteractionCommand:
		root.AllowInChain = false
		root.InputBodyMode = InputBodyArgument
		root.Replies = []*testCommandConfig{newTestCommandConfig(&testExecutionConfig{Index: 1, Keyword: "stop", Command: "stop", AllowInChain: true})}
	}
	return root
}

func TestConversationRouterCachesReplyCommandOnlyAfterQueueingRoot(t *testing.T) {
	root := routerRoot(InteractionCommand)
	queued := false
	c := newTestConversationRouter([]*testCommandConfig{root}, nil, func(*CommandInput) bool { return queued }, 2)
	input := &CommandInput{Text: "run", ConversationID: ConversationID{ChannelID: "C", RootTimestamp: "1"}, MessageID: MessageID{Timestamp: "1"}, AllowedCommandIndexes: []int{0}}
	if result, _ := c.Accept(context.Background(), input); result != AcceptQueueFull {
		t.Fatal("unexpected queue success")
	}
	if _, ok := c.routes.lookup(input.ConversationID); ok {
		t.Fatal("failed root enqueue was cached")
	}
	queued = true
	if result, _ := c.Accept(context.Background(), input); result != AcceptRouted {
		t.Fatal("root was not queued")
	}
	if got, ok := c.routes.lookup(input.ConversationID); !ok || got != c.commands.commands[0] {
		t.Fatalf("route = %v, %v; want reply command", got, ok)
	}
}

func TestConversationRouterCachesNoReplyForOneshotRoot(t *testing.T) {
	root := routerRoot(InteractionOneshot)
	router := newTestConversationRouter([]*testCommandConfig{root}, nil, func(*CommandInput) bool { return true }, 1)
	router.commands.commands[0].replies = nil
	conversation := ConversationID{ChannelID: "C", RootTimestamp: "1"}
	input := &CommandInput{Text: "run", ConversationID: conversation, MessageID: MessageID{Timestamp: "1"}, AllowedCommandIndexes: []int{0}}
	if result, err := router.Accept(context.Background(), input); err != nil || result != AcceptRouted {
		t.Fatalf("root Accept() = %v, %v; want routed", result, err)
	}
	if got, found := router.routes.lookup(conversation); !found || got != nil {
		t.Fatalf("oneshot route = (%v,%v), want negative cache", got, found)
	}
}

func TestConversationRouterNegativeCachesStdinReplyCommand(t *testing.T) {
	root := routerRoot(InteractionStdin)
	router := newTestConversationRouter([]*testCommandConfig{root}, nil, func(*CommandInput) bool { return true }, 1)
	conversation := ConversationID{ChannelID: "C", RootTimestamp: "1"}
	input := &CommandInput{Text: "run", ConversationID: conversation, MessageID: MessageID{Timestamp: "1"}, AllowedCommandIndexes: []int{0}}
	if result, err := router.Accept(context.Background(), input); err != nil || result != AcceptRouted {
		t.Fatalf("root Accept() = %v, %v; want routed", result, err)
	}
	if got, found := router.routes.lookup(conversation); !found || got != nil {
		t.Fatalf("stdin explicit route = (%v,%v), want negative cache", got, found)
	}
}

func TestConversationRoutesCachesNegativeResult(t *testing.T) {
	command := newTestCommand(CommandConfig{}, nil, nil)
	routes := newConversationRoutes(1)
	conversation := ConversationID{ChannelID: "C", RootTimestamp: "1"}
	if got, found := routes.lookup(conversation); found || got != nil {
		t.Fatalf("cache miss = (%v,%v), want (nil,false)", got, found)
	}
	routes.store(conversation, nil)
	if got, found := routes.lookup(conversation); !found || got != nil {
		t.Fatalf("negative cache = (%v,%v), want (nil,true)", got, found)
	}
	routes.store(conversation, command)
	if got, found := routes.lookup(conversation); !found || got != command {
		t.Fatalf("positive cache = (%v,%v), want stored command", got, found)
	}
}

func TestConversationRouterCacheHitDoesNotReResolveRoot(t *testing.T) {
	rootConfig := routerRoot(InteractionCommand)
	var queued *CommandInput
	router := newTestConversationRouter([]*testCommandConfig{rootConfig}, nil, func(input *CommandInput) bool {
		queued = input
		return true
	}, 1)
	conversation := ConversationID{ChannelID: "C", RootTimestamp: "1"}
	if result, err := router.Accept(context.Background(), &CommandInput{Text: "run", ConversationID: conversation, MessageID: MessageID{Timestamp: "1"}, AllowedCommandIndexes: []int{0}}); err != nil || result != AcceptRouted {
		t.Fatalf("root Accept() = %v, %v; want routed", result, err)
	}
	router.commands = NewCommandSet(nil)
	reply := &CommandInput{Text: "stop", ConversationID: conversation, MessageID: MessageID{Timestamp: "2"}, AllowedCommandIndexes: []int{1}}
	if result, err := router.Accept(context.Background(), reply); err != nil || result != AcceptRouted || queued != reply {
		t.Fatalf("cached reply = %v, %v, queued=%p; want reply routed from cached command", result, err, queued)
	}
}

func TestExplicitReplyCommandSelectsOnlySingleNonStdinCommand(t *testing.T) {
	newCommand := func(index int, interactiveStdin bool, hasReplies bool) *Command {
		command := newTestCommand(CommandConfig{
			Index: index, ExecutorConfig: ExecutorConfig{InteractiveStdin: interactiveStdin},
			MatcherConfig: MatcherConfig{RawMatcherConfig: RawMatcherConfig{Keyword: "*"}},
			RunnerConfig:  RunnerConfig{RawRunnerConfig: RawRunnerConfig{Command: "run"}},
		}, nil, nil)
		if hasReplies {
			command.replies = NewCommandSet([]*Command{newTestCommand(CommandConfig{Index: index + 10}, nil, nil)})
		}
		return command
	}
	stdinWithoutReplies := newCommand(0, true, false)
	firstStdin := newCommand(1, true, true)
	secondStdin := newCommand(2, true, true)
	oneshot := newCommand(3, false, false)
	commandInteraction := newCommand(4, false, true)
	tests := []struct {
		name     string
		commands []*Command
		want     *Command
	}{
		{name: "single command reply", commands: []*Command{commandInteraction}, want: commandInteraction},
		{name: "single stdin reply", commands: []*Command{firstStdin}},
		{name: "multiple stdin uses explicit negative cache", commands: []*Command{firstStdin, secondStdin}},
		{name: "skip stdin without replies", commands: []*Command{stdinWithoutReplies, secondStdin}},
		{name: "stdin after oneshot", commands: []*Command{oneshot, secondStdin}},
		{name: "oneshot only", commands: []*Command{oneshot, oneshot}},
		{name: "command interaction only", commands: []*Command{commandInteraction, oneshot}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resolved := make([]ResolvedCommand, len(tt.commands))
			for i, command := range tt.commands {
				resolved[i].Command = command
			}
			if got := explicitReplyCommand(&ResolvedInput{Commands: resolved}); got != tt.want {
				t.Fatalf("explicitReplyCommand() = %p, want %p", got, tt.want)
			}
		})
	}
	if got := explicitReplyCommand(nil); got != nil {
		t.Fatalf("explicitReplyCommand(nil) = %v, want nil", got)
	}
}

func TestConversationRouterAcceptsStdinChainsAndNegativeCachesExplicitReplies(t *testing.T) {
	for i, rootText := range []string{
		"stdin ; stdin",
		"stdin && oneshot",
		"oneshot ; stdin",
		"stdin || oneshot",
		"stdin ; oneshot",
	} {
		for _, history := range []bool{false, true} {
			name := "live"
			if history {
				name = "history"
			}
			t.Run(name+"/"+strconv.Itoa(i)+"/"+rootText, func(t *testing.T) {
				conversation := ConversationID{ChannelID: "C", RootTimestamp: strconv.Itoa(i + 1)}
				store := &StdinStore{}
				stdinReply := newTestCommand(CommandConfig{
					Index: 2, MatcherConfig: MatcherConfig{RawMatcherConfig: RawMatcherConfig{Keyword: "*"}},
					ParserConfig: ParserConfig{InputBodyMode: InputBodyRawStdin},
					RunnerConfig: RunnerConfig{RawRunnerConfig: RawRunnerConfig{Runner: RunnerStdinReply, Command: "stdin-reply"}},
					Dispatch:     DispatchRunner,
				}, NewStdinReplyRunner(), nil)
				oneshot := newTestCommand(CommandConfig{
					Index: 0, MatcherConfig: MatcherConfig{RawMatcherConfig: RawMatcherConfig{Keyword: "oneshot"}},
					ParserConfig: ParserConfig{AllowInChain: true},
					RunnerConfig: RunnerConfig{RawRunnerConfig: RawRunnerConfig{Command: "oneshot"}},
				}, nil, nil)
				stdin := newTestCommand(CommandConfig{
					Index: 1, MatcherConfig: MatcherConfig{RawMatcherConfig: RawMatcherConfig{Keyword: "stdin"}},
					ParserConfig:   ParserConfig{AllowInChain: true},
					ExecutorConfig: ExecutorConfig{InteractiveStdin: true},
					RunnerConfig:   RunnerConfig{RawRunnerConfig: RawRunnerConfig{Command: "stdin"}},
				}, nil, NewCommandSet([]*Command{stdinReply}))
				commands := NewCommandSet([]*Command{oneshot, stdin})
				resolver := RootInputResolver(nil)
				if history {
					resolver = func(context.Context, ConversationID) (RootCommandInput, error) {
						return RootCommandInput{Text: rootText, AllowedCommandIndexes: []int{0, 1}}, nil
					}
				}
				queued := false
				router := NewConversationRouter(store, commands, resolver, newTestDispatcher(func(*CommandInput) bool {
					queued = true
					return true
				}), 1)
				if history {
					reader, writer := io.Pipe()
					endpoint := NewInteractiveStdin(writer, "", func(error) {})
					t.Cleanup(func() {
						endpoint.Close()
						_ = reader.Close()
					})
					store.Lifecycle(conversation).StdinReady(endpoint, stdinReply)
					input := &CommandInput{Text: "hello", ConversationID: conversation, MessageID: MessageID{Timestamp: "reply-" + strconv.Itoa(i+1)}, AllowedCommandIndexes: []int{2}}
					result, err := router.Accept(context.Background(), input)
					if err != nil || result != AcceptRouted || queued {
						t.Fatalf("history reply = %v, %v, queued=%v; want direct stdin delivery", result, err, queued)
					}
					assertStdinReplyDelivered(t, reader, "hello\n")
				} else {
					input := &CommandInput{Text: rootText, ConversationID: conversation, MessageID: MessageID{Timestamp: conversation.RootTimestamp}, AllowedCommandIndexes: []int{0, 1}}
					result, err := router.Accept(context.Background(), input)
					if err != nil || result != AcceptRouted || !queued {
						t.Fatalf("live root = %v, %v, queued=%v; want accepted chain", result, err, queued)
					}
				}
				if !history {
					if cached, found := router.routes.lookup(conversation); !found || cached != nil {
						t.Fatalf("cached explicit reply command = (%p,%v), want negative cache", cached, found)
					}
				}
			})
		}
	}
}

func assertStdinReplyDelivered(t *testing.T, reader io.Reader, want string) {
	t.Helper()
	result := make(chan error, 1)
	go func() {
		got := make([]byte, len(want))
		_, err := io.ReadFull(reader, got)
		if err == nil && string(got) != want {
			err = fmt.Errorf("got %q, want %q", got, want)
		}
		result <- err
	}()
	select {
	case err := <-result:
		if err != nil {
			t.Fatalf("stdin reply delivery: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("stdin reply was not delivered")
	}
}

func TestConversationRouterRejectsChainsContainingCommandInteraction(t *testing.T) {
	newCommand := func(index int, keyword string, allowInChain, interactiveStdin bool) *Command {
		return newTestCommand(CommandConfig{
			Index: index, MatcherConfig: MatcherConfig{RawMatcherConfig: RawMatcherConfig{Keyword: keyword}},
			ParserConfig:   ParserConfig{AllowInChain: allowInChain},
			ExecutorConfig: ExecutorConfig{InteractiveStdin: interactiveStdin},
			RunnerConfig:   RunnerConfig{RawRunnerConfig: RawRunnerConfig{Command: keyword}},
		}, nil, NewCommandSet([]*Command{newTestCommand(CommandConfig{Index: index + 10}, nil, nil)}))
	}
	command := newCommand(0, "command", false, false)
	stdin := newCommand(1, "stdin", true, true)
	oneshot := newCommand(2, "oneshot", true, false)
	commands := NewCommandSet([]*Command{command, stdin, oneshot})
	for _, text := range []string{
		"command ; oneshot",
		"oneshot ; command",
		"command ; command",
		"stdin ; command",
		"command ; stdin",
	} {
		t.Run(text, func(t *testing.T) {
			queued := false
			router := NewConversationRouter(&StdinStore{}, commands, nil, newTestDispatcher(func(*CommandInput) bool {
				queued = true
				return true
			}), 1)
			conversation := ConversationID{ChannelID: "C", RootTimestamp: "1"}
			result, err := router.Accept(context.Background(), &CommandInput{
				Text: text, ConversationID: conversation, MessageID: MessageID{Timestamp: "1"},
				AllowedCommandIndexes: []int{0, 1, 2},
			})
			if err != nil || result != AcceptIgnored || queued {
				t.Fatalf("Accept() = %v, %v, queued=%v; want rejected chain", result, err, queued)
			}
			if _, found := router.routes.lookup(conversation); found {
				t.Fatal("rejected chain was cached")
			}
		})
	}
}

func TestConversationRouterUsesActiveStdinReplyIndexes(t *testing.T) {
	newReply := func(index int, keyword string) *Command {
		return newTestCommand(CommandConfig{
			Index: index, MatcherConfig: MatcherConfig{RawMatcherConfig: RawMatcherConfig{Keyword: keyword}},
			RunnerConfig: RunnerConfig{RawRunnerConfig: RawRunnerConfig{Command: keyword}}, Dispatch: DispatchQueue,
		}, nil, nil)
	}
	first := newTestCommand(CommandConfig{
		Index: 0, MatcherConfig: MatcherConfig{RawMatcherConfig: RawMatcherConfig{Keyword: "stdin"}},
		ParserConfig: ParserConfig{AllowInChain: true}, ExecutorConfig: ExecutorConfig{InteractiveStdin: true},
		RunnerConfig: RunnerConfig{RawRunnerConfig: RawRunnerConfig{Command: "stdin"}},
	}, nil, NewCommandSet([]*Command{newReply(10, "first")}))
	second := newTestCommand(CommandConfig{
		Index: 1, MatcherConfig: MatcherConfig{RawMatcherConfig: RawMatcherConfig{Keyword: "stdin2"}},
		ParserConfig: ParserConfig{AllowInChain: true}, ExecutorConfig: ExecutorConfig{InteractiveStdin: true},
		RunnerConfig: RunnerConfig{RawRunnerConfig: RawRunnerConfig{Command: "stdin2"}},
	}, nil, NewCommandSet([]*Command{newReply(11, "second")}))
	conversation := ConversationID{ChannelID: "C", RootTimestamp: "1"}
	queued := false
	router := NewConversationRouter(&StdinStore{}, NewCommandSet([]*Command{first, second}), nil,
		newTestDispatcher(func(*CommandInput) bool { queued = true; return true }), 1)
	root := &CommandInput{Text: "stdin ; stdin2", ConversationID: conversation, MessageID: MessageID{Timestamp: "1"}, AllowedCommandIndexes: []int{0, 1}}
	if result, err := router.Accept(context.Background(), root); err != nil || result != AcceptRouted {
		t.Fatalf("root = %v, %v; want accepted", result, err)
	}
	if cached, found := router.routes.lookup(conversation); !found || cached != nil {
		t.Fatalf("cached explicit reply command = (%p,%v), want negative cache", cached, found)
	}
	queued = false
	store := router.stdinStore
	reader, writer := io.Pipe()
	endpoint := NewInteractiveStdin(writer, "", func(error) {})
	t.Cleanup(func() { endpoint.Close(); _ = reader.Close() })
	store.Lifecycle(conversation).StdinReady(endpoint, second.replies.commands[0])
	input := &CommandInput{Text: "second", ConversationID: conversation, MessageID: MessageID{Timestamp: "2"}, AllowedCommandIndexes: []int{11}}
	result, err := router.Accept(context.Background(), input)
	if err != nil || result != AcceptRouted || !queued || input.ResolvedInput == nil || input.ResolvedInput.Commands[0].Command != second.replies.commands[0] {
		t.Fatalf("active second stdin reply = %v, %v, queued=%v, resolved=%+v; want second reply %p", result, err, queued, input.ResolvedInput, second.replies.commands[0])
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
	if router.commands.ResolveInput(input.Text, input.AllowedCommandIndexes).Commands[0].Command != nil {
		t.Fatal("test root unexpectedly matched")
	}
	if result, err := router.Accept(context.Background(), input); err != nil || result != AcceptIgnored {
		t.Fatalf("Accept() = %v, %v; want ignored for no matched command", result, err)
	}
	if queued {
		t.Fatal("unmatched root was queued")
	}
	if _, ok := router.routes.lookup(input.ConversationID); ok {
		t.Fatal("unmatched root was cached")
	}
}

func TestConversationRouterDoesNotCacheRootWithParseError(t *testing.T) {
	root := newTestCommandConfig(&testExecutionConfig{Keyword: "run *", Command: "run *"})
	queued := false
	router := newTestConversationRouter([]*testCommandConfig{root}, nil, func(*CommandInput) bool {
		queued = true
		return true
	}, 2)
	conversation := ConversationID{ChannelID: "C", RootTimestamp: "1"}
	input := &CommandInput{
		Text: "run '", ConversationID: conversation, MessageID: MessageID{Timestamp: "1"},
		AllowedCommandIndexes: []int{0},
	}
	if result, err := router.Accept(context.Background(), input); err != nil || result != AcceptRouted || queued {
		t.Fatalf("Accept() = %v, %v, queued=%v; want parse error routed without queue", result, err, queued)
	}
	if input.ResolvedInput == nil || input.ResolvedInput.ParseErr == nil || len(input.ResolvedInput.Commands) == 0 || input.ResolvedInput.Commands[0].Command == nil {
		t.Fatal("matched command or input parse error was not preserved")
	}
	if _, ok := router.routes.lookup(conversation); ok {
		t.Fatal("root with parse error cached ownership")
	}
}

func TestConversationRouterIgnoresReplyWhoseFirstCommandIsUnmatched(t *testing.T) {
	rootConfig := routerRoot(InteractionOneshot)
	rootConfig.Replies = []*testCommandConfig{newTestCommandConfig(&testExecutionConfig{Index: 1, Keyword: "stop", Command: "stop", AllowInChain: true})}
	queued := false
	router := newTestConversationRouter([]*testCommandConfig{rootConfig}, nil, func(*CommandInput) bool {
		queued = true
		return true
	}, 1)
	conversation := ConversationID{ChannelID: "C", RootTimestamp: "1"}
	router.routes.store(conversation, router.commands.commands[0])
	result, err := router.Accept(context.Background(), &CommandInput{
		Text: "unknown ; stop", ConversationID: conversation, MessageID: MessageID{Timestamp: "2"}, AllowedCommandIndexes: []int{1},
	})
	if err != nil || result != AcceptIgnored || queued {
		t.Fatalf("reply Accept() = %v, %v, queued=%v; want ignored without queueing", result, err, queued)
	}
}

func TestConversationRouterRestoresRawRootWithTheSameParseMode(t *testing.T) {
	text := "raw root && \"unfinished\n  payload"
	if _, err := parseCommands("raw root && \"unfinished"); err == nil {
		t.Fatal("test input must fail normal command parsing")
	}
	reply := newTestCommand(CommandConfig{
		Index: 1, MatcherConfig: MatcherConfig{RawMatcherConfig: RawMatcherConfig{Keyword: "reply"}},
		RunnerConfig: RunnerConfig{RawRunnerConfig: RawRunnerConfig{Command: "reply"}},
	}, nil, nil)
	root := newTestCommand(CommandConfig{
		Index: 0, MatcherConfig: MatcherConfig{RawMatcherConfig: RawMatcherConfig{Keyword: "*"}},
		RunnerConfig: RunnerConfig{RawRunnerConfig: RawRunnerConfig{Command: "capture"}},
		ParserConfig: ParserConfig{InputBodyMode: InputBodyRawStdin},
	}, nil, NewCommandSet([]*Command{reply}))
	commands := NewCommandSet([]*Command{root})
	var queued []*CommandInput
	var resolved int
	dispatcher := newTestCommandDispatcher(context.Background(), func(input *CommandInput) bool {
		queued = append(queued, input)
		return true
	})
	router := NewConversationRouter(&StdinStore{}, commands, func(context.Context, ConversationID) (RootCommandInput, error) {
		resolved++
		return RootCommandInput{Text: text, AllowedCommandIndexes: []int{0}}, nil
	}, dispatcher, 0)
	conversation := ConversationID{ChannelID: "C", RootTimestamp: "1"}
	rootInput := &CommandInput{Text: text, ConversationID: conversation, MessageID: MessageID{Timestamp: "1"}, AllowedCommandIndexes: []int{0}}
	acceptRoutedInput(t, router, rootInput)
	assertRawRootInput(t, rootInput.ResolvedInput, root, text)
	if len(queued) != 1 || queued[0] != rootInput {
		t.Fatalf("queued root = %#v, want raw root parse", queued)
	}
	replyInput := &CommandInput{Text: "reply", ConversationID: conversation, MessageID: MessageID{Timestamp: "2"}, AllowedCommandIndexes: []int{1}}
	acceptRoutedInput(t, router, replyInput)
	if resolved != 1 || len(queued) != 2 || queued[1] != replyInput || replyInput.ResolvedInput.Commands[0].Command != root.replies.commands[0] {
		t.Fatalf("resolved=%d queued=%#v reply parsed=%#v, want resolver-restored reply route", resolved, queued, replyInput.ResolvedInput)
	}
}

func assertRawRootInput(t *testing.T, parsed *ResolvedInput, command *Command, text string) {
	t.Helper()
	if parsed.ParseErr != nil || parsed.SingleCommand() != command || parsed.StdinText != text || len(parsed.Commands) != 1 || len(parsed.Commands[0].Part.args) != 1 || parsed.Commands[0].Part.args[0] != text {
		t.Fatalf("root parse = %#v, want normalized raw root input", parsed)
	}
}

func TestConversationRouterResolvesEvictedRootWithOriginalCandidates(t *testing.T) {
	replySet := NewCommandSet([]*Command{newTestCommand(CommandConfig{Index: 3, MatcherConfig: MatcherConfig{RawMatcherConfig: RawMatcherConfig{Keyword: "retry"}}, RunnerConfig: RunnerConfig{RawRunnerConfig: RawRunnerConfig{Command: "retry"}}}, nil, nil)})
	first := newTestCommand(CommandConfig{Index: 1, MatcherConfig: MatcherConfig{RawMatcherConfig: RawMatcherConfig{Keyword: "run"}}, RunnerConfig: RunnerConfig{RawRunnerConfig: RawRunnerConfig{Command: "first"}}}, nil, nil)
	second := newTestCommand(CommandConfig{Index: 2, MatcherConfig: MatcherConfig{RawMatcherConfig: RawMatcherConfig{Keyword: "run"}}, RunnerConfig: RunnerConfig{RawRunnerConfig: RawRunnerConfig{Command: "second"}}}, nil, replySet)
	commands := NewCommandSet([]*Command{first, second})
	var queued *CommandInput
	router := NewConversationRouter(&StdinStore{}, commands, func(context.Context, ConversationID) (RootCommandInput, error) {
		return RootCommandInput{Text: "run", AllowedCommandIndexes: []int{2}}, nil
	}, newTestDispatcher(func(input *CommandInput) bool { queued = input; return true }), 0)
	conversation := ConversationID{ChannelID: "C", RootTimestamp: "1"}
	if result, _ := router.Accept(context.Background(), &CommandInput{Text: "run", ConversationID: conversation, MessageID: MessageID{Timestamp: "1"}, AllowedCommandIndexes: []int{2}}); result != AcceptRouted {
		t.Fatal("AcceptRoot() failed")
	}
	if _, err := router.Accept(context.Background(), &CommandInput{Text: "retry", ConversationID: conversation, MessageID: MessageID{Timestamp: "2"}, AllowedCommandIndexes: []int{3}}); err != nil {
		t.Fatal(err)
	}
	if queued == nil || queued.ResolvedInput.Commands[0].Command != replySet.commands[0] {
		t.Fatalf("thread reply route = %+v, want the ACL-selected root's reply set", queued)
	}
}

func TestConversationRouterCachesReplyCommandRestoredFromHistory(t *testing.T) {
	replySet := NewCommandSet([]*Command{newTestCommand(CommandConfig{
		Index: 1, MatcherConfig: MatcherConfig{RawMatcherConfig: RawMatcherConfig{Keyword: "stop"}},
		RunnerConfig: RunnerConfig{RawRunnerConfig: RawRunnerConfig{Command: "stop"}},
	}, nil, nil)})
	root := newTestCommand(CommandConfig{
		Index: 0, MatcherConfig: MatcherConfig{RawMatcherConfig: RawMatcherConfig{Keyword: "run"}},
		RunnerConfig: RunnerConfig{RawRunnerConfig: RawRunnerConfig{Command: "run"}},
	}, nil, replySet)
	conversation := ConversationID{ChannelID: "C", RootTimestamp: "1"}
	router := NewConversationRouter(&StdinStore{}, NewCommandSet([]*Command{root}), func(context.Context, ConversationID) (RootCommandInput, error) {
		return RootCommandInput{Text: "run", AllowedCommandIndexes: []int{0}}, nil
	}, newTestDispatcher(func(*CommandInput) bool { return true }), 1)
	input := &CommandInput{Text: "stop", ConversationID: conversation, MessageID: MessageID{Timestamp: "2"}, AllowedCommandIndexes: []int{1}}
	if result, err := router.Accept(context.Background(), input); err != nil || result != AcceptRouted {
		t.Fatalf("history reply = %v, %v; want routed", result, err)
	}
	if got, found := router.routes.lookup(conversation); !found || got != root {
		t.Fatalf("history route = (%v,%v), want restored reply command", got, found)
	}
}

func TestConversationRouterPreservesNormalizedCommandRoot(t *testing.T) {
	root := routerRoot(InteractionCommand)
	var queued *CommandInput
	c := newTestConversationRouter([]*testCommandConfig{root}, nil, func(input *CommandInput) bool { queued = input; return true }, 2)
	if result, err := c.Accept(context.Background(), &CommandInput{Text: "run\nbody", ConversationID: ConversationID{ChannelID: "C", RootTimestamp: "1"}, MessageID: MessageID{Timestamp: "1"}, AllowedCommandIndexes: []int{0}}); err != nil || result != AcceptRouted {
		t.Fatalf("Accept() = %v, %v", result, err)
	}
	if queued == nil || queued.Text != "run\nbody" {
		t.Fatalf("queued=%+v", queued)
	}
}

func TestConversationRouterCachesResolverMatch(t *testing.T) {
	root := routerRoot(InteractionOneshot)
	lookups := 0
	c := newTestConversationRouter([]*testCommandConfig{root}, func(context.Context, ConversationID) (RootCommandInput, error) {
		lookups++
		return RootCommandInput{Text: "run", AllowedCommandIndexes: []int{0}}, nil
	}, func(*CommandInput) bool { return true }, 2)
	input := &CommandInput{Text: "reply", ConversationID: ConversationID{ChannelID: "C", RootTimestamp: "1"}, AllowedCommandIndexes: []int{}}
	for range 2 {
		if _, err := c.Accept(context.Background(), input); err != nil {
			t.Fatal(err)
		}
	}
	if lookups != 1 {
		t.Fatalf("resolver calls = %d", lookups)
	}
}

func TestConversationRouterCachesResolverNonMatch(t *testing.T) {
	lookups := 0
	c := newTestConversationRouter(nil, func(context.Context, ConversationID) (RootCommandInput, error) {
		lookups++
		return RootCommandInput{Text: "unknown", AllowedCommandIndexes: []int{0}}, nil
	}, func(*CommandInput) bool { return true }, 2)
	input := &CommandInput{Text: "reply", ConversationID: ConversationID{ChannelID: "C", RootTimestamp: "1"}, AllowedCommandIndexes: []int{}}
	if _, err := c.Accept(context.Background(), input); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Accept(context.Background(), input); err != nil {
		t.Fatal(err)
	}
	if lookups != 1 {
		t.Fatalf("resolver calls = %d", lookups)
	}
}

func TestConversationRouterRejectsDisallowedChainFromHistory(t *testing.T) {
	for _, interaction := range []string{InteractionCommand} {
		t.Run(interaction, func(t *testing.T) {
			root := routerRoot(interaction)
			other := newTestCommandConfig(&testExecutionConfig{Index: 2, Keyword: "other", Command: "other", AllowInChain: true})
			commands := testCommandSet([]*testCommandConfig{root, other}, nil)
			conversation := ConversationID{ChannelID: "C", RootTimestamp: "1"}
			queued := false
			resolverCalls := 0
			dispatcher := newTestDispatcher(func(*CommandInput) bool { queued = true; return true })
			router := NewConversationRouter(&StdinStore{}, commands, func(context.Context, ConversationID) (RootCommandInput, error) {
				resolverCalls++
				return RootCommandInput{Text: "run ; other", AllowedCommandIndexes: []int{0, 2}}, nil
			}, dispatcher, 2)
			for i := 0; i < 2; i++ {
				input := &CommandInput{Text: "stop", ConversationID: conversation, MessageID: MessageID{Timestamp: "2"}, AllowedCommandIndexes: []int{1, 1000}}
				if result, err := router.Accept(context.Background(), input); err != nil || result != AcceptIgnored {
					t.Fatalf("history reply = %v, %v; want ignored", result, err)
				}
				if input.ResolvedInput != nil {
					t.Fatal("reply input was resolved despite the root chain policy")
				}
			}
			if queued {
				t.Fatal("reply to a root rejected by chain policy was queued")
			}
			if cached, ok := router.routes.lookup(conversation); !ok || cached != nil {
				t.Fatalf("invalid history root cache = (%v,%v), want (nil,true)", cached, ok)
			}
			if resolverCalls != 1 {
				t.Fatalf("resolver calls = %d, want one before negative cache hit", resolverCalls)
			}
		})
	}
}

func TestConversationRouterUsesActiveStdinReplyACLWithoutExplicitFallback(t *testing.T) {
	store := &StdinStore{}
	conversation := ConversationID{ChannelID: "C", RootTimestamp: "1"}
	explicitReply := newTestCommand(CommandConfig{
		Index: 1, MatcherConfig: MatcherConfig{RawMatcherConfig: RawMatcherConfig{Keyword: "explicit"}},
		RunnerConfig: RunnerConfig{RawRunnerConfig: RawRunnerConfig{Command: "explicit"}}, Dispatch: DispatchQueue,
	}, nil, nil)
	root := newTestCommand(CommandConfig{
		Index: 0, MatcherConfig: MatcherConfig{RawMatcherConfig: RawMatcherConfig{Keyword: "run"}},
		RunnerConfig: RunnerConfig{RawRunnerConfig: RawRunnerConfig{Command: "run"}},
	}, nil, NewCommandSet([]*Command{explicitReply}))
	activeReply := newTestCommand(CommandConfig{
		Index: 2, MatcherConfig: MatcherConfig{RawMatcherConfig: RawMatcherConfig{Keyword: "active"}},
		RunnerConfig: RunnerConfig{RawRunnerConfig: RawRunnerConfig{Command: "active"}}, Dispatch: DispatchQueue,
	}, nil, nil)
	var queued []*CommandInput
	resolverCalls := 0
	router := NewConversationRouter(store, NewCommandSet([]*Command{root}), func(context.Context, ConversationID) (RootCommandInput, error) {
		resolverCalls++
		return RootCommandInput{Text: "run", AllowedCommandIndexes: []int{0}}, nil
	}, newTestDispatcher(func(input *CommandInput) bool { queued = append(queued, input); return true }), 2)
	if result, err := router.Accept(context.Background(), &CommandInput{Text: "run", ConversationID: conversation, MessageID: MessageID{Timestamp: "1"}, AllowedCommandIndexes: []int{0}}); err != nil || result != AcceptRouted {
		t.Fatalf("root = %v, %v; want routed", result, err)
	}
	reader, writer := io.Pipe()
	endpoint := NewInteractiveStdin(writer, "", func(error) {})
	t.Cleanup(func() { endpoint.Close(); _ = reader.Close() })
	store.Lifecycle(conversation).StdinReady(endpoint, activeReply)
	activeInput := &CommandInput{Text: "active", ConversationID: conversation, MessageID: MessageID{Timestamp: "2"}, AllowedCommandIndexes: []int{2}}
	if result, err := router.Accept(context.Background(), activeInput); err != nil || result != AcceptRouted || activeInput.ResolvedInput == nil || activeInput.ResolvedInput.Commands[0].Command != activeReply {
		t.Fatalf("active ACL reply = %v, %v; resolved=%+v, want command %p", result, err, activeInput.ResolvedInput, activeReply)
	}
	explicitInput := &CommandInput{Text: "explicit", ConversationID: conversation, MessageID: MessageID{Timestamp: "3"}, AllowedCommandIndexes: []int{1}}
	if result, err := router.Accept(context.Background(), explicitInput); err != nil || result != AcceptIgnored || len(queued) != 2 {
		t.Fatalf("explicit ACL fallback = %v, %v; queued=%d, want ignored without fallback", result, err, len(queued))
	}
	if resolverCalls != 0 {
		t.Fatalf("history resolver calls = %d, want zero while active endpoint exists", resolverCalls)
	}
	uncachedConversation := ConversationID{ChannelID: "C", RootTimestamp: "uncached"}
	uncachedReader, uncachedWriter := io.Pipe()
	uncachedEndpoint := NewInteractiveStdin(uncachedWriter, "", func(error) {})
	t.Cleanup(func() { uncachedEndpoint.Close(); _ = uncachedReader.Close() })
	store.Lifecycle(uncachedConversation).StdinReady(uncachedEndpoint, activeReply)
	uncachedInput := &CommandInput{Text: "active", ConversationID: uncachedConversation, MessageID: MessageID{Timestamp: "reply"}, AllowedCommandIndexes: []int{2}}
	if result, err := router.Accept(context.Background(), uncachedInput); err != nil || result != AcceptRouted || resolverCalls != 0 {
		t.Fatalf("uncached active reply = %v, %v; resolver calls=%d", result, err, resolverCalls)
	}
}

func TestConversationRouterUsesSameReplyResultForCachedAndRestoredChain(t *testing.T) {
	first := newTestCommand(CommandConfig{Index: 0, MatcherConfig: MatcherConfig{RawMatcherConfig: RawMatcherConfig{Keyword: "first"}}, RunnerConfig: RunnerConfig{RawRunnerConfig: RawRunnerConfig{Command: "first"}}, ParserConfig: ParserConfig{AllowInChain: true}}, nil, nil)
	second := newTestCommand(CommandConfig{Index: 1, MatcherConfig: MatcherConfig{RawMatcherConfig: RawMatcherConfig{Keyword: "second"}}, RunnerConfig: RunnerConfig{RawRunnerConfig: RawRunnerConfig{Command: "second"}}, ParserConfig: ParserConfig{AllowInChain: true}}, nil, nil)
	commands := NewCommandSet([]*Command{first, second})
	conversation := ConversationID{ChannelID: "C", RootTimestamp: "1"}
	rootInput := RootCommandInput{Text: "first ; second", AllowedCommandIndexes: []int{0, 1}}
	var outcomes []AcceptResult
	for _, cached := range []bool{true, false} {
		resolverCalls := 0
		router := NewConversationRouter(&StdinStore{}, commands, func(context.Context, ConversationID) (RootCommandInput, error) {
			resolverCalls++
			return rootInput, nil
		}, newTestDispatcher(func(*CommandInput) bool { return true }), 1)
		if cached {
			router.routes.store(conversation, nil)
		}
		result, err := router.Accept(context.Background(), &CommandInput{Text: "reply", ConversationID: conversation, MessageID: MessageID{Timestamp: "2"}, AllowedCommandIndexes: []int{2}})
		if err != nil {
			t.Fatal(err)
		}
		outcomes = append(outcomes, result)
		if cached && resolverCalls != 0 || !cached && resolverCalls != 1 {
			t.Fatalf("cache=%v resolver calls=%d", cached, resolverCalls)
		}
		stored, ok := router.routes.lookup(conversation)
		if !ok || stored != nil {
			t.Fatalf("cache=%v reply command = %v, %v; want negative cache", cached, stored, ok)
		}
	}
	if outcomes[0] != AcceptIgnored || outcomes[1] != outcomes[0] {
		t.Fatalf("cached/restored reply outcomes = %v, want both ignored", outcomes)
	}
}

func TestConversationRouterEvictsLeastRecentlyUsedRoute(t *testing.T) {
	root := routerRoot(InteractionOneshot)
	lookups := 0
	c := newTestConversationRouter([]*testCommandConfig{root}, func(context.Context, ConversationID) (RootCommandInput, error) {
		lookups++
		return RootCommandInput{Text: "run", AllowedCommandIndexes: []int{0}}, nil
	}, func(*CommandInput) bool { return true }, 2)
	for _, thread := range []string{"A", "B", "A", "C", "B"} {
		if _, err := c.Accept(context.Background(), &CommandInput{Text: "reply", ConversationID: ConversationID{ChannelID: "C", RootTimestamp: thread}, AllowedCommandIndexes: []int{}}); err != nil {
			t.Fatal(err)
		}
	}
	if lookups != 4 {
		t.Fatalf("resolver calls = %d, want 4", lookups)
	}
}

func TestConversationRouterDoesNotCacheResolverError(t *testing.T) {
	lookups := 0
	c := newTestConversationRouter(nil, func(context.Context, ConversationID) (RootCommandInput, error) {
		lookups++
		return RootCommandInput{}, errors.New("failed")
	}, func(*CommandInput) bool { return true }, 2)
	input := &CommandInput{Text: "reply", ConversationID: ConversationID{ChannelID: "C", RootTimestamp: "1"}, AllowedCommandIndexes: []int{}}
	for range 2 {
		if _, err := c.Accept(context.Background(), input); err == nil {
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
	c := newTestConversationRouter([]*testCommandConfig{root}, func(context.Context, ConversationID) (RootCommandInput, error) {
		return RootCommandInput{Text: "run", AllowedCommandIndexes: []int{0}}, nil
	}, func(input *CommandInput) bool { queued = input; return true }, 2)
	result, err := c.Accept(context.Background(), &CommandInput{Text: "stop && stop\nbody", ConversationID: ConversationID{ChannelID: "C", RootTimestamp: "1"}, AllowedCommandIndexes: []int{1}})
	if err != nil {
		t.Fatal(err)
	}
	if result != AcceptRouted {
		t.Fatalf("result=%v", result)
	}
	if queued == nil {
		t.Fatal("reply was not queued")
	}
	if queued.ResolvedInput.Commands[0].Command != c.commands.commands[0].replies.commands[0] || queued.Text != "stop && stop\nbody" {
		t.Fatalf("queued=%+v", queued)
	}
	c.dispatcher = newTestDispatcher(func(*CommandInput) bool { return false })
	result, err = c.Accept(context.Background(), &CommandInput{Text: "stop", ConversationID: ConversationID{ChannelID: "C", RootTimestamp: "1"}, AllowedCommandIndexes: []int{1}})
	if err != nil || result != AcceptQueueFull {
		t.Fatalf("result=%v err=%v", result, err)
	}
}

func TestConversationRouterDispatchesMalformedExplicitReplyWithoutQueue(t *testing.T) {
	root := routerRoot(InteractionCommand)
	root.Replies[0].Keyword = "stop *"
	var queued *CommandInput
	router := newTestConversationRouter([]*testCommandConfig{root}, func(context.Context, ConversationID) (RootCommandInput, error) {
		return RootCommandInput{Text: "run", AllowedCommandIndexes: []int{0}}, nil
	}, func(input *CommandInput) bool {
		queued = input
		return true
	}, 1)
	result, err := router.Accept(context.Background(), &CommandInput{
		Text:                  `stop "unbalanced`,
		ConversationID:        ConversationID{ChannelID: "C", RootTimestamp: "1"},
		MessageID:             MessageID{Timestamp: "2"},
		AllowedCommandIndexes: []int{1},
	})
	if err != nil || result != AcceptRouted || queued != nil {
		t.Fatalf("Accept() = %v, %v; queued = %+v, want direct parse-error output", result, err, queued)
	}
}

func TestConversationRouterIgnoresOneshotReply(t *testing.T) {
	root := routerRoot(InteractionOneshot)
	c := newTestConversationRouter([]*testCommandConfig{root}, func(context.Context, ConversationID) (RootCommandInput, error) {
		return RootCommandInput{Text: "run", AllowedCommandIndexes: []int{0}}, nil
	}, func(*CommandInput) bool { t.Fatal("oneshot reply queued"); return false }, 2)
	result, err := c.Accept(context.Background(), &CommandInput{Text: "reply", ConversationID: ConversationID{ChannelID: "C", RootTimestamp: "1"}, AllowedCommandIndexes: []int{}})
	if err != nil || result != AcceptIgnored {
		t.Fatalf("result=%v err=%v", result, err)
	}
}

func TestConversationRouterDropsStdinReplyWithoutEndpoint(t *testing.T) {
	for _, rootText := range []string{"run", "run ; other"} {
		t.Run(rootText, func(t *testing.T) {
			root := routerRoot(InteractionStdin)
			other := newTestCommandConfig(&testExecutionConfig{Index: 1, Keyword: "other", Command: "other", AllowInChain: true})
			commands := testCommandSet([]*testCommandConfig{root, other}, nil)
			indexes := []int{0}
			if rootText == "run ; other" {
				indexes = []int{0, 1}
			}
			queued := false
			router := NewConversationRouter(&StdinStore{}, commands, func(context.Context, ConversationID) (RootCommandInput, error) {
				return RootCommandInput{Text: rootText, AllowedCommandIndexes: indexes}, nil
			}, newTestDispatcher(func(*CommandInput) bool { queued = true; return true }), 2)
			conversation := ConversationID{ChannelID: "C", RootTimestamp: "1"}
			result, err := router.Accept(context.Background(), &CommandInput{Text: "reply", ConversationID: conversation, AllowedCommandIndexes: []int{1000}})
			if err != nil || result != AcceptIgnored || queued {
				t.Fatalf("result=%v err=%v queued=%v", result, err, queued)
			}
			if cached, found := router.routes.lookup(conversation); !found || cached != nil {
				t.Fatalf("cached explicit reply command = (%p,%v), want negative cache", cached, found)
			}
		})
	}
}

func TestConversationRouterRoutesRawStdinReply(t *testing.T) {
	root := routerRoot(InteractionStdin)
	ctx := ConversationID{ChannelID: "C", RootTimestamp: "1"}
	store := &StdinStore{}
	commands := testCommandSet([]*testCommandConfig{root}, nil)
	commands.commands[0].replies.commands[0].runner = NewStdinReplyRunner()
	c := NewConversationRouter(store, commands, func(context.Context, ConversationID) (RootCommandInput, error) {
		return RootCommandInput{Text: "run", AllowedCommandIndexes: []int{0}}, nil
	}, newTestDispatcher(func(*CommandInput) bool { t.Fatal("stdin reply queued"); return false }), 2)
	reader, writer := io.Pipe()
	endpoint := NewInteractiveStdin(writer, "", func(error) {})
	t.Cleanup(func() {
		endpoint.Close()
		_ = reader.Close()
	})
	store.Lifecycle(ctx).StdinReady(endpoint, commands.commands[0].replies.commands[0])
	for _, body := range []string{"<@BOT> “raw” &amp;", "hello world", "yes && no", "yes || no", "foo ; bar", `unbalanced "quote`, "first line\nsecond line"} {
		result, err := c.Accept(context.Background(), &CommandInput{Text: body, ConversationID: ctx, AllowedCommandIndexes: []int{1000}})
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

func TestRouterDirectDispatchUsesCommandRunnerAndKeepsWholeBody(t *testing.T) {
	store := &StdinStore{}
	conversation := ConversationID{ChannelID: "C", RootTimestamp: "1"}
	stdinReply := newTestCommand(CommandConfig{
		Index:         1,
		MatcherConfig: MatcherConfig{RawMatcherConfig: RawMatcherConfig{Keyword: "*"}},
		ParserConfig:  ParserConfig{InputBodyMode: InputBodyRawStdin},
		RunnerConfig:  RunnerConfig{RawRunnerConfig: RawRunnerConfig{Runner: RunnerStdinReply, Command: "stdin-reply"}},
		Dispatch:      DispatchRunner,
	}, NewStdinReplyRunner(), nil)
	root := newTestCommand(CommandConfig{Index: 0, MatcherConfig: MatcherConfig{RawMatcherConfig: RawMatcherConfig{Keyword: "run"}}, RunnerConfig: RunnerConfig{RawRunnerConfig: RawRunnerConfig{Command: "run"}}}, nil, NewCommandSet([]*Command{stdinReply}))
	queued := 0
	commands := NewCommandSet([]*Command{root})
	router := NewConversationRouter(store, commands, nil, newTestDispatcher(func(*CommandInput) bool {
		queued++
		return true
	}), 1)
	if result, err := router.Accept(context.Background(), &CommandInput{Text: "run", ConversationID: conversation, MessageID: MessageID{Timestamp: "1"}, AllowedCommandIndexes: []int{0}}); err != nil || result != AcceptRouted {
		t.Fatalf("root command = %v, %v", result, err)
	}
	reader, writer := io.Pipe()
	endpoint := NewInteractiveStdin(writer, "", func(error) {})
	t.Cleanup(func() {
		endpoint.Close()
		_ = reader.Close()
	})
	store.Lifecycle(conversation).StdinReady(endpoint, stdinReply)
	for i, tc := range []struct{ body, want string }{
		{body: "yes && no\nunbalanced \"quote", want: "yes && no\nunbalanced \"quote\n"},
		{body: "  'quoted' \t \n", want: "  'quoted' \t \n"},
		{body: "", want: "\n"},
		{body: "line1\nline2\n", want: "line1\nline2\n"},
	} {
		input := &CommandInput{Text: tc.body, ConversationID: conversation, MessageID: MessageID{Timestamp: strconv.Itoa(i + 2)}, AllowedCommandIndexes: []int{1}}
		if result, err := router.Accept(context.Background(), input); err != nil || result != AcceptRouted || queued != 1 {
			t.Fatalf("stdin reply body %q = %v, %v; queue calls=%d", tc.body, result, err, queued)
		}
		read := make(chan string, 1)
		go func() {
			got := make([]byte, len(tc.want))
			_, _ = io.ReadFull(reader, got)
			read <- string(got)
		}()
		select {
		case got := <-read:
			if got != tc.want {
				t.Fatalf("stdin reply body = %q; want %q", got, tc.want)
			}
		case <-time.After(time.Second):
			t.Fatalf("stdin reply body %q was not delivered", tc.body)
		}
	}
}

func TestRouterDirectRootRunsWithWholeRawInput(t *testing.T) {
	text := `first && second ; third || "unfinished` + "\n  raw body\t"
	runner := &fakeRunner{}
	command := newTestCommand(CommandConfig{
		Index: 0, MatcherConfig: MatcherConfig{RawMatcherConfig: RawMatcherConfig{Keyword: "*"}},
		RunnerConfig: RunnerConfig{RawRunnerConfig: RawRunnerConfig{Command: "capture original"}},
		ParserConfig: ParserConfig{InputBodyMode: InputBodyRawStdin}, Dispatch: DispatchRunner,
	}, runner, nil)
	commands := NewCommandSet([]*Command{command})
	queued := false
	router := NewConversationRouter(&StdinStore{}, commands, nil, newTestDispatcher(func(*CommandInput) bool {
		queued = true
		return true
	}), 1)
	input := &CommandInput{Text: text, ConversationID: ConversationID{ChannelID: "C", RootTimestamp: "1"}, MessageID: MessageID{Timestamp: "1"}, AllowedCommandIndexes: []int{0}}
	if result, err := router.Accept(context.Background(), input); err != nil || result != AcceptRouted || queued {
		t.Fatalf("direct root = %v, %v; queued=%v", result, err, queued)
	}
	if calls := runner.Calls(); len(calls) != 1 || calls[0].name != "capture" {
		t.Fatalf("runner calls = %#v, want one direct raw call", calls)
	}
	if calls := runner.Calls(); !slices.Equal(calls[0].args, []string{"original"}) {
		t.Fatalf("runner args = %#v, want configured args without conversation metadata", calls[0].args)
	}
	if inputs := runner.Inputs(); len(inputs) != 1 || inputs[0] != text {
		t.Fatalf("stdin = %#v, want full raw text", inputs)
	}
}

func TestDispatcherUsesQueueFullForRootAndReplyWhenQueueUnavailable(t *testing.T) {
	reply := newTestCommand(CommandConfig{
		Index:         1,
		MatcherConfig: MatcherConfig{RawMatcherConfig: RawMatcherConfig{Keyword: "stop"}},
		RunnerConfig:  RunnerConfig{RawRunnerConfig: RawRunnerConfig{Command: "stop"}},
	}, nil, nil)
	root := newTestCommand(CommandConfig{
		Index:         0,
		MatcherConfig: MatcherConfig{RawMatcherConfig: RawMatcherConfig{Keyword: "run"}},
		RunnerConfig:  RunnerConfig{RawRunnerConfig: RawRunnerConfig{Command: "run"}},
	}, nil, NewCommandSet([]*Command{reply}))
	commands := NewCommandSet([]*Command{root})
	dispatcher := newTestDispatcher(nil)
	conversation := ConversationID{ChannelID: "C", RootTimestamp: "1"}

	rootRouter := NewConversationRouter(&StdinStore{}, commands, nil, dispatcher, 1)
	if result, _ := rootRouter.Accept(context.Background(), &CommandInput{Text: "run", ConversationID: conversation, MessageID: MessageID{Timestamp: "1"}, AllowedCommandIndexes: []int{0}}); result != AcceptQueueFull {
		t.Fatalf("root Accept() = %v, want queue full", result)
	}

	replyRouter := NewConversationRouter(&StdinStore{}, commands, func(context.Context, ConversationID) (RootCommandInput, error) {
		return RootCommandInput{Text: "run", AllowedCommandIndexes: []int{0}}, nil
	}, dispatcher, 1)
	if result, err := replyRouter.Accept(context.Background(), &CommandInput{Text: "stop", ConversationID: conversation, MessageID: MessageID{Timestamp: "2"}, AllowedCommandIndexes: []int{1}}); err != nil || result != AcceptQueueFull {
		t.Fatalf("reply Accept() = %v, %v; want queue full", result, err)
	}
}

type dispatcherExitCodeRunner int

func (r dispatcherExitCodeRunner) CommandContext(context.Context, string, ...string) Cmd {
	return &fakeCmd{exitCode: int(r)}
}

func TestDispatcherIgnoresFailedDirectReply(t *testing.T) {
	reply := newTestCommand(CommandConfig{
		Index:         1,
		MatcherConfig: MatcherConfig{RawMatcherConfig: RawMatcherConfig{Keyword: "stop"}},
		RunnerConfig:  RunnerConfig{RawRunnerConfig: RawRunnerConfig{Command: "stop"}},
		Dispatch:      DispatchRunner,
	}, dispatcherExitCodeRunner(1), nil)
	root := newTestCommand(CommandConfig{
		Index:         0,
		MatcherConfig: MatcherConfig{RawMatcherConfig: RawMatcherConfig{Keyword: "run"}},
		RunnerConfig:  RunnerConfig{RawRunnerConfig: RawRunnerConfig{Command: "run"}},
	}, nil, NewCommandSet([]*Command{reply}))
	queued := false
	commands := NewCommandSet([]*Command{root})
	router := NewConversationRouter(&StdinStore{}, commands, func(context.Context, ConversationID) (RootCommandInput, error) {
		return RootCommandInput{Text: "run", AllowedCommandIndexes: []int{0}}, nil
	}, newTestDispatcher(func(*CommandInput) bool { queued = true; return true }), 1)
	result, err := router.Accept(context.Background(), &CommandInput{Text: "stop", ConversationID: ConversationID{ChannelID: "C", RootTimestamp: "1"}, MessageID: MessageID{Timestamp: "2"}, AllowedCommandIndexes: []int{1}})
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
	outputs := make(chan *observedOutput, 20)
	queueCalls := 0
	reply := newTestCommand(CommandConfig{
		Index:         1,
		MatcherConfig: MatcherConfig{RawMatcherConfig: RawMatcherConfig{Keyword: "lookup *"}},
		RunnerConfig:  RunnerConfig{RawRunnerConfig: RawRunnerConfig{Runner: RunnerHTTP, Method: "GET", URL: server.URL + "/?q=*"}},
		Dispatch:      DispatchExecutor,
	}, NewHTTPRunner(RunnerConfig{RawRunnerConfig: RawRunnerConfig{Runner: RunnerHTTP, Method: "GET", URL: server.URL + "/?q=*"}}), nil)
	root := newTestCommand(CommandConfig{Index: 0, MatcherConfig: MatcherConfig{RawMatcherConfig: RawMatcherConfig{Keyword: "run"}}, RunnerConfig: RunnerConfig{RawRunnerConfig: RawRunnerConfig{Command: "run"}}}, nil, NewCommandSet([]*Command{reply}))
	commands := NewCommandSet([]*Command{root})
	observeCommandSet(commands, outputs)
	dispatcher := NewCommandDispatcher(context.Background(), NewExecutor(), &StdinStore{}, &ConversationLocks{}, func(*CommandInput) bool {
		queueCalls++
		return true
	})
	router := NewConversationRouter(&StdinStore{}, commands, nil, dispatcher, 1)
	conversation := ConversationID{ChannelID: "C", RootTimestamp: "1"}
	if result, err := router.Accept(context.Background(), &CommandInput{Text: "run", ConversationID: conversation, MessageID: MessageID{Timestamp: "1"}, AllowedCommandIndexes: []int{0}}); err != nil || result != AcceptRouted {
		t.Fatalf("root command = %v, %v", result, err)
	}
	queueCalls = 0
	input := &CommandInput{Text: "lookup value", ConversationID: conversation, MessageID: MessageID{Timestamp: "2"}, AllowedCommandIndexes: []int{1}}
	if result, err := router.Accept(context.Background(), input); err != nil || result != AcceptRouted || queueCalls != 0 || input.ResolvedInput.Commands[0].Command != root.replies.commands[0] {
		t.Fatalf("HTTP reply = %v, %v; queue calls=%d", result, err, queueCalls)
	}
	dispatcher.Close()
	dispatcher.Wait()
	if len(requestStarted) == 0 {
		t.Fatal("HTTP reply request did not start")
	}
	assertHTTPReplyOutputs(t, outputs, conversation, "2")
}

func assertHTTPReplyOutputs(t *testing.T, outputs chan *observedOutput, conversation ConversationID, messageTimestamp string) {
	t.Helper()
	var body strings.Builder
	spawned, finished := false, false
	for len(outputs) > 0 {
		output := <-outputs
		if output.ConversationID != conversation || output.MessageID.Timestamp != messageTimestamp {
			t.Fatalf("output context = %+v/%+v", output.ConversationID, output.MessageID)
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
	outputs := make(chan *observedOutput, 20)
	reply := newTestCommand(CommandConfig{
		Index:         1,
		MatcherConfig: MatcherConfig{RawMatcherConfig: RawMatcherConfig{Keyword: "stop"}},
		RunnerConfig:  RunnerConfig{RawRunnerConfig: RawRunnerConfig{Command: "stop"}},
	}, nil, nil)
	httpConfig := RunnerConfig{RawRunnerConfig: RawRunnerConfig{Runner: RunnerHTTP, Method: "GET", URL: server.URL}}
	root := newTestCommand(CommandConfig{
		Index:         0,
		MatcherConfig: MatcherConfig{RawMatcherConfig: RawMatcherConfig{Keyword: "lookup"}},
		RunnerConfig:  httpConfig, Dispatch: DispatchExecutor,
	}, NewHTTPRunner(httpConfig), NewCommandSet([]*Command{reply}))
	commands := NewCommandSet([]*Command{root})
	queueCalls := 0
	observeCommandSet(commands, outputs)
	dispatcher := NewCommandDispatcher(context.Background(), NewExecutor(), &StdinStore{}, &ConversationLocks{}, func(*CommandInput) bool {
		queueCalls++
		return false
	})
	router := NewConversationRouter(&StdinStore{}, commands, nil, dispatcher, 1)
	defer func() { dispatcher.Close(); dispatcher.Wait() }()
	conversation := ConversationID{ChannelID: "C", RootTimestamp: "1"}
	if result, err := router.Accept(context.Background(), &CommandInput{Text: "lookup", ConversationID: conversation, MessageID: MessageID{Timestamp: "1"}, AllowedCommandIndexes: []int{0}}); err != nil || result != AcceptRouted {
		t.Fatalf("HTTP root Accept() = %v, %v; want routed despite full queue", result, err)
	}
	replyInput := &CommandInput{Text: "stop", ConversationID: conversation, MessageID: MessageID{Timestamp: "2"}, AllowedCommandIndexes: []int{1}}
	if result, err := router.Accept(context.Background(), replyInput); err != nil || result != AcceptQueueFull || replyInput.ResolvedInput.Commands[0].Command != root.replies.commands[0] {
		t.Fatalf("cached root reply = %v, %v; want queue full and cached reply command", result, err)
	}
	if queueCalls != 1 {
		t.Fatalf("queue calls = %d, want only the queued reply", queueCalls)
	}
	for len(outputs) > 0 {
		<-outputs
	}
}

func TestStdinReplyCommandSucceedsWhenEndpointRejectsInput(t *testing.T) {
	endpoint := newInteractiveStdinSession("", 0, nil)
	runner := NewStdinReplyRunner()
	if err := endpoint.TrySend("buffered"); err != nil {
		t.Fatal(err)
	}
	if err := endpoint.TrySend("probe"); err != ErrInteractiveStdinBusy {
		t.Fatalf("second TrySend() error = %v, want full-buffer error", err)
	}
	run := func() int {
		command := runner.CommandContext(context.Background(), "stdin-reply")
		command.(interface{ SetStdinTarget(*InteractiveStdin) }).SetStdinTarget(endpoint)
		command.SetStdin(strings.NewReader("reply"))
		return command.Run()
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

package command

import (
	"context"
	"testing"
	"time"
)

type switchingStdinReplyRunner struct {
	store        *StdinStore
	conversation ConversationID
	reply        *Command
	second       *InteractiveStdin
	closeFirst   bool
	args         []string
}

func (r *switchingStdinReplyRunner) CommandContext(ctx context.Context, wildcard []string, hasWildcard bool) Cmd {
	r.args = append([]string(nil), wildcard...)
	first, _ := r.store.lookup(r.conversation)
	r.store.register(r.conversation, r.second, r.reply)
	if r.closeFirst {
		first.endpoint.Close()
	}
	return NewStdinReplyRunner().CommandContext(ctx, wildcard, hasWildcard)
}

func TestRouterKeepsSelectedStdinEndpointAcrossStoreSwitch(t *testing.T) {
	for _, closeFirst := range []bool{false, true} {
		t.Run(map[bool]string{false: "open", true: "closed"}[closeFirst], func(t *testing.T) {
			store := &StdinStore{}
			conversation := ConversationID{ChannelID: "C", RootTimestamp: "1"}
			first := newInteractiveStdinSession("", 0, nil)
			second := newInteractiveStdinSession("", 0, nil)
			t.Cleanup(func() { first.Close(); second.Close() })
			runner := &switchingStdinReplyRunner{store: store, conversation: conversation, second: second, closeFirst: closeFirst}
			reply := newTestCommand(testExecutionConfig{
				Index: 1, Keyword: "*", InputBodyMode: InputBodyRawStdin, Runner: RunnerStdinReply, Command: "stdin-reply",
				Dispatch: DispatchRunner,
			}, runner, nil)
			runner.reply = reply
			store.register(conversation, first, reply)
			outputs := make(chan *observedOutput, 1)
			queueCalls := 0
			observeCommandSet(NewCommandSet([]*Command{reply}), outputs)
			dispatcher := NewCommandDispatcher(context.Background(), NewExecutor(), store, &ConversationLocks{}, func(*CommandInput) bool {
				queueCalls++
				return true
			})
			defer func() { dispatcher.Close(); dispatcher.Wait() }()
			router := NewConversationRouter(store, NewCommandSet(nil), nil, dispatcher, 1)
			input := &CommandInput{Text: "reply body", ConversationID: conversation, MessageID: MessageID{Timestamp: "2"}, AllowedCommandIndexes: []int{1}}
			if result, err := router.Accept(context.Background(), input); err != nil || result != AcceptRouted {
				t.Fatalf("Accept() = %v, %v; want routed", result, err)
			}
			if len(runner.args) != 0 {
				t.Errorf("runner args = %v, want no ConversationID arguments", runner.args)
			}
			if closeFirst {
				if got := len(first.replies); got != 0 {
					t.Errorf("closed endpoint received %d replies, want none", got)
				}
			} else {
				select {
				case got := <-first.replies:
					if got != "reply body" {
						t.Errorf("first endpoint received %q, want reply body", got)
					}
				case <-time.After(time.Second):
					t.Fatal("selected endpoint did not receive the reply")
				}
			}
			if got := len(second.replies); got != 0 {
				t.Errorf("replacement endpoint received %d replies, want none", got)
			}
			if queueCalls != 0 {
				t.Errorf("queue calls = %d, want direct runner dispatch", queueCalls)
			}
		})
	}
}

func TestDirectStdinReplyIgnoresMissingTargetOrSetter(t *testing.T) {
	config := testExecutionConfig{Runner: RunnerStdinReply, Command: "stdin-reply"}
	input := &CommandInput{Text: "reply"}
	command := newTestCommand(config, NewStdinReplyRunner(), nil)
	if got := runDirectCommand(ResolvedCommand{Command: command, StdinText: "reply"}, input); got != DispatchIgnored {
		t.Fatalf("runDirectCommand() without target = %v, want ignored", got)
	}

	endpoint := newInteractiveStdinSession("", 0, nil)
	t.Cleanup(endpoint.Close)
	input.stdinTarget = endpoint
	runner := &fakeRunner{}
	command = newTestCommand(config, runner, nil)
	if got := runDirectCommand(ResolvedCommand{Command: command, StdinText: "reply"}, input); got != DispatchIgnored {
		t.Fatalf("runDirectCommand() without target setter = %v, want ignored", got)
	}
	if got := runner.Inputs(); len(got) != 0 {
		t.Fatalf("runner inputs = %v, want Cmd.Run not called", got)
	}
}

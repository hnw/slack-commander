package cmd

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

func (r *switchingStdinReplyRunner) CommandContext(ctx context.Context, name string, args ...string) Cmd {
	r.args = append([]string(nil), args...)
	first, _ := r.store.lookup(r.conversation)
	r.store.register(r.conversation, r.second, r.reply)
	if r.closeFirst {
		first.endpoint.Close()
	}
	return NewStdinReplyRunner().CommandContext(ctx, name, args...)
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
			reply := NewCommand(CommandConfig{
				Index:         1,
				MatcherConfig: MatcherConfig{RawMatcherConfig: RawMatcherConfig{Keyword: "*"}},
				ParserConfig:  ParserConfig{InputBodyMode: InputBodyRawStdin},
				RunnerConfig:  RunnerConfig{RawRunnerConfig: RawRunnerConfig{Runner: RunnerStdinReply, Command: "stdin-reply"}},
				Dispatch:      DispatchRunner,
			}, runner, nil)
			runner.reply = reply
			store.register(conversation, first, reply)
			outputs := make(chan *CommandOutput, 1)
			queueCalls := 0
			dispatcher := NewCommandDispatcher(context.Background(), NewExecutor(outputs), outputs, store, &ConversationLocks{}, func(*CommandInput) bool {
				queueCalls++
				return true
			})
			defer func() { dispatcher.Close(); dispatcher.Wait() }()
			router := NewConversationRouter(store, NewCommandSet(nil), nil, dispatcher, 1)
			input := &CommandInput{Text: "reply body", ConversationID: conversation, MessageID: MessageID{Timestamp: "2"}, AllowedCommandIndexes: []int{1}}
			if result, err := router.Accept(input); err != nil || result != AcceptRouted {
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
	config := CommandConfig{RunnerConfig: RunnerConfig{RawRunnerConfig: RawRunnerConfig{Runner: RunnerStdinReply, Command: "stdin-reply"}}}
	input := &CommandInput{Text: "reply"}
	command := NewCommand(config, NewStdinReplyRunner(), nil)
	if got := runDirectCommand(command, []string{"stdin-reply"}, input); got != DispatchIgnored {
		t.Fatalf("runDirectCommand() without target = %v, want ignored", got)
	}

	endpoint := newInteractiveStdinSession("", 0, nil)
	t.Cleanup(endpoint.Close)
	input.stdinTarget = endpoint
	runner := &fakeRunner{}
	command.runner = runner
	if got := runDirectCommand(command, []string{"stdin-reply"}, input); got != DispatchIgnored {
		t.Fatalf("runDirectCommand() without target setter = %v, want ignored", got)
	}
	if got := runner.Inputs(); len(got) != 0 {
		t.Fatalf("runner inputs = %v, want Cmd.Run not called", got)
	}
}

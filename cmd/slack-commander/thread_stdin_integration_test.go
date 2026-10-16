package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hnw/slack-commander/internal/command"
	"github.com/hnw/slack-commander/internal/messaging"
	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
	"github.com/slack-go/slack/socketmode"
)

func TestSlackThreadStdinWithConcurrentExecWorkers(t *testing.T) {
	for _, rootText := range []string{"agent\nold", "quick ; agent"} {
		t.Run(strings.ReplaceAll(strings.ReplaceAll(rootText, " ", "_"), "\n", "_"), func(t *testing.T) {
			outputs := make(chan *observedCommandOutput, 30)

			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = io.WriteString(w, `{"ok":true,"user_id":"U-self","bot_id":"B-self"}`)
			}))
			defer server.Close()
			smc := socketmode.New(slack.New("test", slack.OptionAPIURL(server.URL+"/")))
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			requests := make(chan *command.CommandInput, 10)

			stdinStore := &command.StdinStore{}
			stdinReply := command.NewCommand(command.CommandConfig{Index: 2, ParserConfig: command.ParserConfig{}, Dispatch: command.DispatchRunner}, command.NewMatcher(command.MatcherConfig{RawMatcherConfig: command.RawMatcherConfig{Keyword: "*"}, InputBodyMode: command.InputBodyRawStdin}), command.NewStdinReplyRunner(), nil, testOutputFactory(outputs)(messaging.ReplyConfig{}, 0))
			oneshot := command.NewCommand(command.CommandConfig{Index: 0, ParserConfig: command.ParserConfig{AllowInChain: true}}, command.NewMatcher(command.MatcherConfig{RawMatcherConfig: command.RawMatcherConfig{Keyword: "quick"}}), command.NewExecRunner(command.RunnerConfig{RawRunnerConfig: command.RawRunnerConfig{Command: "/bin/true"}}), nil, testOutputFactory(outputs)(messaging.ReplyConfig{}, 0))
			commandLine := `/bin/sh -c 'IFS= read -r first; printf "ready\n"; IFS= read -r second; printf "%s|%s\n" "$first" "$second"'`
			if !strings.Contains(rootText, "\n") {
				commandLine = strings.Replace(commandLine, "IFS= read -r first", "first=old", 1)
			}
			root := command.NewCommand(command.CommandConfig{Index: 1, ParserConfig: command.ParserConfig{AllowInChain: true}, ExecutorConfig: command.ExecutorConfig{Timeout: 10 * time.Second, InteractiveStdin: true}}, command.NewMatcher(command.MatcherConfig{RawMatcherConfig: command.RawMatcherConfig{Keyword: "agent"}}), command.NewExecRunner(command.RunnerConfig{RawRunnerConfig: command.RawRunnerConfig{Command: commandLine}}), command.NewCommandSet([]*command.Command{stdinReply}), testOutputFactory(outputs)(messaging.ReplyConfig{}, messaging.DefaultOutputFlushInterval))
			commands := command.NewCommandSet([]*command.Command{oneshot, root})
			var queuedCount atomic.Int64
			queued := make(chan struct{}, 10)
			conversationLocks := &command.ConversationLocks{}
			router := command.NewConversationRouter(stdinStore, commands, nil, newMainTestDispatcher(context.Background(), stdinStore, conversationLocks, func(input *command.CommandInput) bool {
				queuedCount.Add(1)
				select {
				case requests <- input:
					queued <- struct{}{}
					return true
				default:
					return false
				}
			}), 1)
			var workers sync.WaitGroup

			startWorkers(ctx, 2, requests, stdinStore, conversationLocks, command.NewExecutor(), &workers)
			listenerDone := make(chan struct{})
			go func() {
				if err := messaging.SlackListener(ctx, smc, messaging.Config{
					AllowedUserIDs: []string{"U"}, AllowedChannelIDs: []string{"C"},
					ListenerConfigs: []messaging.ListenerConfig{{CommandIndex: 0, RawListenerConfig: messaging.RawListenerConfig{AllowedUserIDs: []string{"U"}, AllowedChannelIDs: []string{"C"}}}, {CommandIndex: 1, RawListenerConfig: messaging.RawListenerConfig{AllowedUserIDs: []string{"U"}, AllowedChannelIDs: []string{"C"}}}, {CommandIndex: 2, IsReply: true, RawListenerConfig: messaging.RawListenerConfig{AllowedUserIDs: []string{"U"}, AllowedChannelIDs: []string{"C"}}}},
				}, router); err != nil {
					t.Errorf("SlackListener() error = %v", err)
				}
				close(listenerDone)
			}()
			t.Cleanup(func() { cancel(); <-listenerDone; workers.Wait() })
			send := func(root, text string) {
				timestamp := "1"
				if root != "" {
					timestamp = "2"
				}
				smc.Events <- socketmode.Event{Type: socketmode.EventTypeEventsAPI, Data: slackevents.EventsAPIEvent{
					Type: slackevents.CallbackEvent, InnerEvent: slackevents.EventsAPIInnerEvent{Type: "message", Data: &slackevents.MessageEvent{
						Channel: "C", User: "U", TimeStamp: timestamp, ThreadTimeStamp: root, Text: text,
					}},
				}}
			}
			send("", rootText)
			select {
			case <-queued:
			case <-time.After(5 * time.Second):
				t.Fatal("root command was not queued")
			}
			awaitThreadStdinReady(t, outputs)
			queuedRootText := strings.Replace(rootText, "\nold", "\nqueued behind the active command", 1)
			send("", queuedRootText)
			select {
			case <-queued:
			case <-time.After(5 * time.Second):
				t.Fatal("second same-conversation command was not queued")
			}
			send("1", "<@BOT> “raw” &amp;")
			awaitThreadStdinOutput(t, outputs, "old| \"raw\" &\n")
			if got := queuedCount.Load(); got != 2 {
				t.Fatalf("queue received %d inputs, want only the two root commands", got)
			}
			select {
			case <-queued:
				t.Fatal("synthetic stdin reply was queued as a command")
			default:
			}
		})
	}
}

func awaitThreadStdinReady(t *testing.T, outputs <-chan *observedCommandOutput) {
	t.Helper()
	timeout := time.NewTimer(5 * time.Second)
	defer timeout.Stop()
	for {
		select {
		case output := <-outputs:
			if output.Text == "ready\n" {
				return
			}
			if output.Finished {
				t.Fatalf("command exited before stdin was ready: %+v", output)
			}
		case <-timeout.C:
			t.Fatal("stdin did not become ready")
		}
	}
}

func awaitThreadStdinOutput(t *testing.T, outputs <-chan *observedCommandOutput, want string) {
	t.Helper()
	timeout := time.NewTimer(5 * time.Second)
	defer timeout.Stop()
	var got strings.Builder
	for {
		select {
		case output := <-outputs:
			got.WriteString(output.Text)
			if output.ConversationID.ChannelID != "C" ||
				output.ConversationID.RootTimestamp != "1" {
				t.Fatal("wrong output thread")
			}
			if output.Finished {
				if output.ExitCode != 0 || got.String() != want {
					t.Fatalf("exit=%d output=%q", output.ExitCode, got.String())
				}
				return
			}
		case <-timeout.C:
			t.Fatalf("missing output %q, got %q", want, got.String())
		}
	}
}

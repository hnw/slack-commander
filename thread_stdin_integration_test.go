package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hnw/slack-commander/cmd"
	"github.com/hnw/slack-commander/pubsub"
	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
	"github.com/slack-go/slack/socketmode"
)

func TestSlackThreadStdinWithConcurrentExecWorkers(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"ok":true,"user_id":"U-self","bot_id":"B-self"}`)
	}))
	defer server.Close()
	smc := socketmode.New(slack.New("test", slack.OptionAPIURL(server.URL+"/")))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	requests := make(chan *cmd.CommandInput, 10)
	outputs := make(chan *cmd.CommandOutput, 30)
	root := cmd.NewCommand(cmd.CommandConfig{MatcherConfig: cmd.MatcherConfig{Keyword: "agent"}, RunnerConfig: cmd.RunnerConfig{Command: `/bin/sh -c 'IFS= read -r first; printf "ready\n"; IFS= read -r second; printf "%s|%s\n" "$first" "$second"'`}, ExecutorConfig: cmd.ExecutorConfig{Timeout: 10, InteractiveStdin: true}, OutputFlushInterval: cmd.DefaultOutputFlushInterval, ThreadReplyMode: cmd.ThreadReplyStdin}, cmd.NewExecRunner(), nil)
	commands := cmd.NewCommandSet([]*cmd.Command{root})
	coordinator := cmd.NewConversationCoordinatorWithRootInputResolver(commands, nil, func(input *cmd.CommandInput) bool {
		select {
		case requests <- input:
			return true
		default:
			return false
		}
	}, 1)
	var workers sync.WaitGroup
	startWorkers(ctx, 2, requests, coordinator, commands, outputs, &workers)
	listenerDone := make(chan struct{})
	go func() {
		if err := pubsub.SlackListener(ctx, smc, pubsub.Config{
			AllowedUserIDs: []string{"U"}, AllowedChannelIDs: []string{"C"},
			ListenerConfigs: []pubsub.ListenerConfig{{CommandIndex: 0, AllowedUserIDs: []string{"U"}, AllowedChannelIDs: []string{"C"}}},
		}, coordinator); err != nil {
			t.Errorf("SlackListener() error = %v", err)
		}
		close(listenerDone)
	}()
	t.Cleanup(func() { cancel(); <-listenerDone; workers.Wait() })
	send := func(root, text string) {
		smc.Events <- socketmode.Event{Type: socketmode.EventTypeEventsAPI, Data: slackevents.EventsAPIEvent{
			Type: slackevents.CallbackEvent, InnerEvent: slackevents.EventsAPIInnerEvent{Type: "message", Data: &slackevents.MessageEvent{
				Channel: "C", User: "U", TimeStamp: "1", ThreadTimeStamp: root, Text: text,
			}},
		}}
	}
	send("", "agent\nold")
	awaitThreadStdinReady(t, outputs)
	send("1", "<@BOT> “raw” &amp;")
	awaitThreadStdinOutput(t, outputs, "old| \"raw\" &\n")
}

func awaitThreadStdinReady(t *testing.T, outputs <-chan *cmd.CommandOutput) {
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

func awaitThreadStdinOutput(t *testing.T, outputs <-chan *cmd.CommandOutput, want string) {
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

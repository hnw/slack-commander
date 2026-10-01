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
	stdinReply := cmd.NewCommand(cmd.CommandConfig{Index: 1, MatcherConfig: cmd.MatcherConfig{Keyword: "*"}, SyntheticStdinReply: true}, nil, nil)
	root := cmd.NewCommand(cmd.CommandConfig{MatcherConfig: cmd.MatcherConfig{Keyword: "agent"}, RunnerConfig: cmd.RunnerConfig{Command: `/bin/sh -c 'IFS= read -r first; printf "ready\n"; IFS= read -r second; printf "%s|%s\n" "$first" "$second"'`}, ExecutorConfig: cmd.ExecutorConfig{Timeout: 10, InteractiveStdin: true}, OutputFlushInterval: cmd.DefaultOutputFlushInterval}, cmd.NewExecRunner(), cmd.NewCommandSet([]*cmd.Command{stdinReply}))
	commands := cmd.NewCommandSet([]*cmd.Command{root})
	var queuedCount atomic.Int64
	queued := make(chan struct{}, 10)
	stdinStore := &cmd.StdinStore{}
	conversationLocks := &cmd.ConversationLocks{}
	router := cmd.NewConversationRouterWithRootInputResolver(commands, nil, func(input *cmd.CommandInput) bool {
		queuedCount.Add(1)
		select {
		case requests <- input:
			queued <- struct{}{}
			return true
		default:
			return false
		}
	}, 1, stdinStore)
	var workers sync.WaitGroup
	startWorkers(ctx, 2, requests, stdinStore, conversationLocks, commands, outputs, &workers)
	listenerDone := make(chan struct{})
	go func() {
		if err := pubsub.SlackListener(ctx, smc, pubsub.Config{
			AllowedUserIDs: []string{"U"}, AllowedChannelIDs: []string{"C"},
			ListenerConfigs: []pubsub.ListenerConfig{{CommandIndex: 0, RawListenerConfig: pubsub.RawListenerConfig{AllowedUserIDs: []string{"U"}, AllowedChannelIDs: []string{"C"}}}, {CommandIndex: 1, IsReply: true, RawListenerConfig: pubsub.RawListenerConfig{AllowedUserIDs: []string{"U"}, AllowedChannelIDs: []string{"C"}}}},
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
	send("", "agent\nold")
	select {
	case <-queued:
	case <-time.After(5 * time.Second):
		t.Fatal("root command was not queued")
	}
	awaitThreadStdinReady(t, outputs)
	send("", "agent\nqueued behind the active command")
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

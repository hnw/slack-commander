package main

import (
	"context"
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
	smc := socketmode.New(slack.New("test"))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	requests := make(chan *cmd.CommandInput, 10)
	outputs := make(chan *cmd.CommandOutput, 30)
	root := cmd.NewCommandConfig(&cmd.ExecutionConfig{
		Keyword: "agent", Command: `/bin/sh -c 'IFS= read -r first; printf "ready\n"; IFS= read -r second; printf "%s|%s\n" "$first" "$second"'`, Timeout: 10,
	})
	root.AllowInChain = false
	root.InteractiveStdin = true
	root.ThreadReplyMode = cmd.ThreadReplyStdin
	configs := []*cmd.CommandConfig{root}
	coordinator := cmd.NewConversationCoordinator(configs, nil, func(input *cmd.CommandInput) bool {
		select {
		case requests <- input:
			return true
		default:
			return false
		}
	}, pubsub.NormalizeCommandFirstLine, 1)
	var workers sync.WaitGroup
	for range 2 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			cmd.ExecutorWithCoordinator(
				ctx,
				requests,
				outputs,
				cmd.ExecutionConfigs(configs),
				nil,
				coordinator,
			)
		}()
	}
	listenerDone := make(chan struct{})
	go func() {
		pubsub.SlackListener(ctx, smc, pubsub.Config{
			AllowedUserIDs: []string{
				"U",
			}, AllowedChannelIDs: []string{"C"},
		}, coordinator)
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
	awaitThreadStdinOutput(t, outputs, "old|<@BOT> “raw” &amp;\n")
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
			if output.ConversationContext.ChannelID != "C" ||
				output.ConversationContext.RootThreadTimestamp != "1" {
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

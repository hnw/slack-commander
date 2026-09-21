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
		_, _ = io.WriteString(
			w,
			`{"ok":true,"messages":[{"ts":"1","text":"agent"},{"ts":"2","user":"U","text":"resume"}]}`,
		)
	}))
	defer server.Close()
	smc := socketmode.New(slack.New("test", slack.OptionAPIURL(server.URL+"/")))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var registry cmd.ThreadInputRegistry
	requests := make(chan *cmd.CommandInput, 10)
	outputs := make(chan *cmd.CommandOutput, 30)
	configs := []*cmd.CommandConfig{cmd.NewCommandConfig(&cmd.Definition{
		Keyword: "agent", Command: `/bin/sh -c 'IFS= read -r first; IFS= read -r second; printf "%s|%s\n" "$first" "$second"'`, Timeout: 10,
	}, nil)}
	var workers sync.WaitGroup
	for range 2 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			cmd.ExecutorWithThreadInput(ctx, requests, outputs, configs, nil, &registry)
		}()
	}
	listenerDone := make(chan struct{})
	go func() {
		pubsub.SlackListenerWithThreadInput(ctx, smc, requests, pubsub.Config{
			AllowedUserIDs: []string{
				"U",
			}, AllowedChannelIDs: []string{"C"}, AcceptThreadMessage: true,
		}, configs, &registry)
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
	key := cmd.ThreadKey{ChannelID: "C", RootThreadTimestamp: "1"}
	send("", "agent\nold")
	old := awaitInteractiveStdinEndpoint(t, &registry, key, nil)
	send("", "agent\nnew")
	latest := awaitInteractiveStdinEndpoint(t, &registry, key, old)
	if err := old.TrySend("finish-old"); err != nil {
		t.Fatal(err)
	}
	awaitThreadStdinOutput(t, outputs, "old|finish-old\n")
	if registry.Lookup(key) != latest {
		t.Fatal("old exit removed latest endpoint")
	}
	send("1", "<@BOT> “raw” &amp;")
	awaitThreadStdinOutput(t, outputs, "new|<@BOT> “raw” &amp;\n")
	if registry.Lookup(key) != nil {
		t.Fatal("endpoint survived process exit")
	}
	send("1", "agent\nafter\nexit")
	awaitThreadStdinOutput(t, outputs, "after|exit\n")
}

func awaitInteractiveStdinEndpoint(
	t *testing.T,
	registry *cmd.ThreadInputRegistry,
	key cmd.ThreadKey,
	previous *cmd.InteractiveStdin,
) *cmd.InteractiveStdin {
	t.Helper()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	timeout := time.NewTimer(3 * time.Second)
	defer timeout.Stop()
	for {
		if endpoint := registry.Lookup(key); endpoint != nil && endpoint != previous {
			return endpoint
		}
		select {
		case <-ticker.C:
		case <-timeout.C:
			t.Fatal("endpoint not published")
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

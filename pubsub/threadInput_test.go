package pubsub

import (
	"bufio"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/hnw/slack-commander/cmd"
	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
	"github.com/slack-go/slack/socketmode"
)

func TestThreadReplyUsesOnlyRootCommandReplyRules(t *testing.T) {
	lookups := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lookups++
		if r.URL.Path != "/conversations.replies" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"ok":true,"messages":[{"ts":"1","text":"todo <https://example.com|item>"}]}`))
	}))
	defer server.Close()
	smc := socketmode.New(slack.New("test", slack.OptionAPIURL(server.URL+"/")))
	root := cmd.NewCommandConfig(&cmd.Definition{Keyword: "todo *", Command: "todo-wrapper *"}, nil)
	reply := cmd.NewCommandConfig(&cmd.Definition{Keyword: "cancel", Command: "todo-wrapper --cancel"}, nil)
	root.Replies = []*cmd.CommandConfig{reply}
	queue := make(chan *cmd.CommandInput, 1)
	onMessageEventWithCommands(smc, &slackevents.MessageEvent{
		Channel: "C", User: "U", TimeStamp: "2", ThreadTimeStamp: "1", Text: "<@BOT> “cancel”",
	}, queue, Config{AllowedUserIDs: []string{"U"}, AllowedChannelIDs: []string{"C"}}, nil, []*cmd.CommandConfig{root})
	select {
	case input := <-queue:
		if input.Text != ` "cancel"` || len(input.CommandConfigs) != 1 || input.CommandConfigs[0] != reply {
			t.Fatalf("input = %+v", input)
		}
	case <-time.After(time.Second):
		t.Fatal("reply command was not queued")
	}
	if lookups != 1 {
		t.Fatalf("lookups = %d, want 1", lookups)
	}
}

func TestThreadReplyIgnoresCommandChainAndMessageEdits(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"ok":true,"messages":[{"ts":"1","text":"todo one && todo two"}]}`))
	}))
	defer server.Close()
	smc := socketmode.New(slack.New("test", slack.OptionAPIURL(server.URL+"/")))
	root := cmd.NewCommandConfig(&cmd.Definition{Keyword: "todo *", Command: "todo-wrapper *"}, nil)
	root.Replies = []*cmd.CommandConfig{cmd.NewCommandConfig(&cmd.Definition{Keyword: "cancel", Command: "todo-wrapper --cancel"}, nil)}
	queue := make(chan *cmd.CommandInput, 1)
	cfg := Config{AllowedUserIDs: []string{"U"}, AllowedChannelIDs: []string{"C"}}
	onMessageEventWithCommands(smc, &slackevents.MessageEvent{
		Channel: "C", User: "U", TimeStamp: "2", ThreadTimeStamp: "1", Text: "cancel",
	}, queue, cfg, nil, []*cmd.CommandConfig{root})
	onMessageEventWithCommands(smc, &slackevents.MessageEvent{
		Channel: "C", User: "U", TimeStamp: "3", Text: "todo item", SubType: slack.MsgSubTypeMessageChanged,
	}, queue, cfg, nil, []*cmd.CommandConfig{root})
	select {
	case input := <-queue:
		t.Fatalf("unexpected input = %+v", input)
	default:
	}
}

func TestThreadInputRoutesRawTextWithoutFallback(t *testing.T) {
	for _, mention := range []bool{false, true} {
		var registry cmd.ThreadInputRegistry
		r, w := io.Pipe()
		endpoint := cmd.NewInteractiveStdin(w, "initial", func(err error) { t.Error(err) })
		key := cmd.ThreadKey{ChannelID: "C", RootThreadTimestamp: "1"}
		registry.Register(key, endpoint)
		queue := make(chan *cmd.CommandInput, 10)
		cfg := Config{AllowedUserIDs: []string{"U"}, AllowedChannelIDs: []string{"C"}}
		text := "<@BOT> “hello” &amp; <https://example.com|label>"
		send := func(text, user string) {
			if mention {
				onAppMentionEvent(
					nil,
					&slackevents.AppMentionEvent{
						Channel:         "C",
						User:            user,
						ThreadTimeStamp: "1",
						Text:            text,
					},
					queue,
					cfg,
					&registry,
				)
			} else {
				onMessageEvent(
					nil,
					&slackevents.MessageEvent{
						Channel:         "C",
						User:            user,
						ThreadTimeStamp: "1",
						Text:            text,
					},
					queue,
					cfg,
					&registry,
				)
			}
		}
		send("unauthorized", "other")
		send(text, "U")
		send("dropped", "U")
		reader := bufio.NewReader(r)
		for _, want := range []string{"initial\n", text + "\n"} {
			got, err := reader.ReadString('\n')
			if err != nil || got != want {
				t.Fatalf("got=%q err=%v", got, err)
			}
		}
		endpoint.Close()
		send("closed", "U")
		_ = r.Close()
		if len(queue) != 0 {
			t.Fatal("live reply entered command queue")
		}
	}
}

func TestAbsentThreadInputDoesNotUseGlobalThreadRouting(t *testing.T) {
	smc := socketmode.New(slack.New("test"))
	cfg := Config{
		AllowedUserIDs:    []string{"U"},
		AllowedChannelIDs: []string{"C"},
	}
	var registry cmd.ThreadInputRegistry
	queue := make(chan *cmd.CommandInput, 1)
	event := &slackevents.MessageEvent{
		Channel:         "C",
		User:            "U",
		TimeStamp:       "2",
		ThreadTimeStamp: "1",
		Text:            "reply",
	}
	onMessageEvent(smc, event, queue, cfg, &registry)
	select {
	case input := <-queue:
		t.Fatalf("unexpected global input=%+v", input)
	default:
	}
}

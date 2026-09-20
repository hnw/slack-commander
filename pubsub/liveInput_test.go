package pubsub

import (
	"bufio"
	"bytes"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hnw/slack-commander/cmd"
	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
	"github.com/slack-go/slack/socketmode"
)

func TestLiveReplyRoutesRawTextAndDropsWithoutHistory(t *testing.T) {
	for _, mention := range []bool{false, true} {
		var registry cmd.LiveInputRegistry
		r, w := io.Pipe()
		endpoint := cmd.NewLiveInput(w, "initial", func(err error) { t.Error(err) })
		key := cmd.ThreadKey{ChannelID: "C", RootThreadTimestamp: "1"}
		registry.Register(key, endpoint)
		queue := make(chan *cmd.CommandInput, 10)
		cfg := Config{AllowedUserIDs: []string{"U"}, AllowedChannelIDs: []string{"C"}}
		text := "<@BOT> “hello” &amp; <https://example.com|label>"
		var logs bytes.Buffer
		old := log.Writer()
		log.SetOutput(&logs)
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
					nil,
					&registry,
				)
			}
		}
		send("unauthorized", "other")
		send(text, "U")
		send("dropped", "U")
		log.SetOutput(old)
		if !strings.Contains(logs.String(), "drop") ||
			!strings.Contains(logs.String(), "buffer is full") {
			t.Fatalf("logs=%s", logs.String())
		}
		if len(queue) != 0 {
			t.Fatal("live reply entered command queue")
		}
		reader := bufio.NewReader(r)
		for _, want := range []string{"initial\n", text + "\n"} {
			got, err := reader.ReadString('\n')
			if err != nil || got != want {
				t.Fatalf("got=%q err=%v", got, err)
			}
		}
		endpoint.Close()
		_ = r.Close()
	}
}

func TestAbsentLiveInputUsesExistingThreadRouting(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/conversations.replies" {
			t.Errorf("unexpected API %s", r.URL.Path)
		}
		_, _ = io.WriteString(
			w,
			`{"ok":true,"messages":[{"ts":"1","text":"agent"},{"ts":"2","user":"U","text":"reply"}]}`,
		)
	}))
	defer server.Close()
	smc := socketmode.New(slack.New("test", slack.OptionAPIURL(server.URL+"/")))
	cfg := Config{AllowedUserIDs: []string{"U"}, AllowedChannelIDs: []string{"C"}}
	var registry cmd.LiveInputRegistry
	queue := make(chan *cmd.CommandInput, 1)
	configs := []*cmd.CommandConfig{
		cmd.NewCommandConfig(
			&cmd.Definition{
				Keyword:      "agent",
				Command:      "agent",
				Continuation: cmd.ContinuationThread,
			},
			nil,
		),
	}
	event := &slackevents.MessageEvent{
		Channel:         "C",
		User:            "U",
		TimeStamp:       "2",
		ThreadTimeStamp: "1",
		Text:            "reply",
	}
	onMessageEvent(smc, event, queue, cfg, configs, &registry)
	select {
	case input := <-queue:
		if !input.ThreadContinuation {
			t.Fatal("not continuation")
		}
	default:
		t.Fatal("missing continuation")
	}
	cfg.AcceptThreadMessage = true
	onMessageEvent(smc, event, queue, cfg, nil, &registry)
	select {
	case input := <-queue:
		if input.ThreadContinuation || input.Text != "reply" {
			t.Fatalf("input=%+v", input)
		}
	default:
		t.Fatal("missing ordinary thread command")
	}
}

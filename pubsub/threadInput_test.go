package pubsub

import (
	"bufio"
	"io"
	"testing"

	"github.com/hnw/slack-commander/cmd"
	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
	"github.com/slack-go/slack/socketmode"
)

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

func TestAbsentThreadInputUsesGlobalThreadRouting(t *testing.T) {
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
		if input.Text != "reply" {
			t.Fatalf("input=%+v", input)
		}
	default:
		t.Fatal("missing ordinary thread command")
	}
}

package pubsub

import (
	"context"
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

func TestSlackListenerCancelsThreadRootRequestOnShutdown(t *testing.T) {
	for _, tc := range []struct {
		name  string
		event interface{}
	}{
		{"message", &slackevents.MessageEvent{User: "U-other", Channel: "C", TimeStamp: "2", ThreadTimeStamp: "1", Text: "retry"}},
		{"app mention", &slackevents.AppMentionEvent{User: "U-other", Channel: "C", TimeStamp: "2", ThreadTimeStamp: "1", Text: "<@BOT> retry"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			previousUserID, previousBotID := userID, ownBotID
			t.Cleanup(func() { userID, ownBotID = previousUserID, previousBotID })
			started, canceled, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/auth.test":
					_, _ = io.WriteString(w, `{"ok":true,"user_id":"U-self","bot_id":"B-self"}`)
				case "/conversations.replies":
					if err := r.ParseForm(); err != nil {
						t.Error(err)
						return
					}
					close(started)
					select {
					case <-r.Context().Done():
						close(canceled)
					case <-release:
						_, _ = io.WriteString(w, `{"ok":true,"messages":[{"ts":"1","user":"U-other","text":"run"}]}`)
					}
				default:
					t.Errorf("unexpected Slack API path: %s", r.URL.Path)
				}
			}))
			defer server.Close()
			smc := socketmode.New(slack.New("test", slack.OptionAPIURL(server.URL+"/")))
			output := NewSlackOutput(100)
			reply := cmd.NewCommand(cmd.CommandConfig{Index: 1, MatcherConfig: cmd.MatcherConfig{RawMatcherConfig: cmd.RawMatcherConfig{Keyword: "retry"}}}, nil, nil, output.NewCommandOutput(ReplyConfig{}, 0))
			root := cmd.NewCommand(cmd.CommandConfig{MatcherConfig: cmd.MatcherConfig{RawMatcherConfig: cmd.RawMatcherConfig{Keyword: "run"}}}, nil, cmd.NewCommandSet([]*cmd.Command{reply}), output.NewCommandOutput(ReplyConfig{}, 0))
			cfg := Config{ListenerConfigs: []ListenerConfig{{CommandIndex: 0}, {CommandIndex: 1, IsReply: true}}}
			queued := false
			router := cmd.NewConversationRouter(nil, cmd.NewCommandSet([]*cmd.Command{root}), SlackRootInputResolver(smc, cfg), newSlackTestDispatcher(func(*cmd.CommandInput) bool {
				queued = true
				return true
			}), 1)
			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan struct{})
			var listenerErr error
			go func() {
				listenerErr = SlackListener(ctx, smc, cfg, router)
				close(done)
			}()
			defer func() {
				cancel()
				close(release)
				<-done
			}()
			smc.Events <- socketmode.Event{Type: socketmode.EventTypeEventsAPI, Data: slackevents.EventsAPIEvent{
				Type: slackevents.CallbackEvent, InnerEvent: slackevents.EventsAPIInnerEvent{Data: tc.event},
			}}
			select {
			case <-started:
			case <-time.After(3 * time.Second):
				t.Fatal("thread root request did not start")
			}
			cancel()
			select {
			case <-done:
			case <-time.After(3 * time.Second):
				t.Fatal("SlackListener did not stop during thread root lookup")
			}
			if listenerErr != nil {
				t.Fatalf("SlackListener() error = %v", listenerErr)
			}
			select {
			case <-canceled:
			case <-time.After(3 * time.Second):
				t.Fatal("Slack API request was not canceled")
			}
			if queued {
				t.Fatal("reply was queued after canceled root lookup")
			}
		})
	}
}

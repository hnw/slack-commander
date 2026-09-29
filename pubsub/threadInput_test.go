package pubsub

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hnw/slack-commander/cmd"
	"github.com/slack-go/slack"
	"github.com/slack-go/slack/socketmode"
)

func TestSlackRootTextResolverFetchesAndNormalizesRootText(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/conversations.replies" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"ok":true,"messages":[{"ts":"1","text":"todo <https://example.com|item>"}]}`))
	}))
	defer server.Close()

	smc := socketmode.New(slack.New("test", slack.OptionAPIURL(server.URL+"/")))
	resolve := SlackRootTextResolver(smc)
	text, err := resolve(cmd.ConversationID{ChannelID: "C", RootTimestamp: "1"})
	if err != nil {
		t.Fatal(err)
	}
	if text != "todo item" {
		t.Fatalf("text = %q", text)
	}
}

func TestSlackRootTextResolverNormalizesReminderRootText(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"ok":true,"messages":[{"ts":"1","user":"USLACKBOT","text":"Reminder: todo <https://example.com|item>."}]}`))
	}))
	defer server.Close()

	smc := socketmode.New(slack.New("test", slack.OptionAPIURL(server.URL+"/")))
	text, err := SlackRootTextResolver(smc)(cmd.ConversationID{ChannelID: "C", RootTimestamp: "1"})
	if err != nil {
		t.Fatal(err)
	}
	if text != "todo item" {
		t.Fatalf("text = %q", text)
	}
}

func TestNormalizeSlackTextPreservesBody(t *testing.T) {
	if got := normalizeSlackText("<@BOT> \u201ccancel\u201d\n\u201craw\u201d &amp;"); got != " \"cancel\"\n\"raw\" &" {
		t.Fatalf("text = %q", got)
	}
}

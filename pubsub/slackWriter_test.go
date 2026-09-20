package pubsub

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hnw/slack-commander/cmd"
	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
	"github.com/slack-go/slack/socketmode"
)

func boolPtr(value bool) *bool {
	return &value
}

func TestGetThreadTimestampUsesConversationRoot(t *testing.T) {
	output := &cmd.CommandOutput{
		ReplyInfo: &slackevents.MessageEvent{
			Channel:         "C123",
			TimeStamp:       "1700000000.000200",
			ThreadTimeStamp: "1700000000.000100",
		},
		ReplyConfig: &ReplyConfig{},
		ConversationContext: cmd.ConversationContext{
			ChannelID:           "C123",
			RootThreadTimestamp: "1700000000.000100",
		},
	}

	if got := getThreadTimestamp(output); got != "1700000000.000100" {
		t.Fatalf("thread timestamp = %q", got)
	}
}

func TestGetThreadTimestampFallsBackToTriggeringMessage(t *testing.T) {
	output := &cmd.CommandOutput{
		ReplyInfo:   &slackevents.MessageEvent{Channel: "C123", TimeStamp: "1700000000.000200"},
		ReplyConfig: &ReplyConfig{},
	}

	if got := getThreadTimestamp(output); got != "1700000000.000200" {
		t.Fatalf("thread timestamp = %q", got)
	}
}

func TestGetReplyBroadcastDefaultsToTrue(t *testing.T) {
	output := &cmd.CommandOutput{ReplyConfig: &ReplyConfig{}}

	if !getReplyBroadcast(output) {
		t.Fatal("reply broadcast should default to true")
	}
}

func TestGetReplyBroadcastHonorsExplicitFalse(t *testing.T) {
	output := &cmd.CommandOutput{
		ReplyConfig: &ReplyConfig{ReplyBroadcast: boolPtr(false)},
	}

	if getReplyBroadcast(output) {
		t.Fatal("reply broadcast should be disabled when explicitly false")
	}
}

func TestPostMessagePostsOneRootThreadReply(t *testing.T) {
	var requestCount int
	var gotChannel, gotThreadTimestamp, gotBroadcast string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		if err := r.ParseForm(); err != nil {
			t.Fatalf("ParseForm() error = %v", err)
		}
		gotChannel = r.Form.Get("channel")
		gotThreadTimestamp = r.Form.Get("thread_ts")
		gotBroadcast = r.Form.Get("reply_broadcast")
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "channel": "C123", "ts": "1700000000.000300"})
	}))
	defer server.Close()

	smc := socketmode.New(slack.New("token", slack.OptionAPIURL(server.URL+"/")))
	output := &cmd.CommandOutput{
		ReplyInfo:   &slackevents.MessageEvent{Channel: "C123", TimeStamp: "1700000000.000200"},
		ReplyConfig: &ReplyConfig{},
		ConversationContext: cmd.ConversationContext{
			ChannelID:           "C123",
			RootThreadTimestamp: "1700000000.000100",
		},
		Text: "output",
	}

	if err := postMessage(smc, output); err != nil {
		t.Fatalf("postMessage() error = %v", err)
	}
	if requestCount != 1 {
		t.Fatalf("post count = %d, want 1", requestCount)
	}
	if gotChannel != "C123" {
		t.Fatalf("channel = %q, want C123", gotChannel)
	}
	if gotThreadTimestamp != "1700000000.000100" {
		t.Fatalf("thread_ts = %q, want root timestamp", gotThreadTimestamp)
	}
	if gotBroadcast != "true" {
		t.Fatalf("reply_broadcast = %q, want true", gotBroadcast)
	}
}

func TestPostMessageDisablesBroadcastWhenExplicitlyFalse(t *testing.T) {
	var requestCount int
	var gotThreadTimestamp, gotBroadcast string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		if err := r.ParseForm(); err != nil {
			t.Fatalf("ParseForm() error = %v", err)
		}
		gotThreadTimestamp = r.Form.Get("thread_ts")
		gotBroadcast = r.Form.Get("reply_broadcast")
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "channel": "C123", "ts": "1700000000.000300"})
	}))
	defer server.Close()

	smc := socketmode.New(slack.New("token", slack.OptionAPIURL(server.URL+"/")))
	output := &cmd.CommandOutput{
		ReplyInfo:   &slackevents.MessageEvent{Channel: "C123", TimeStamp: "1700000000.000200"},
		ReplyConfig: &ReplyConfig{ReplyBroadcast: boolPtr(false)},
		ConversationContext: cmd.ConversationContext{
			ChannelID:           "C123",
			RootThreadTimestamp: "1700000000.000100",
		},
		Text: "output",
	}

	if err := postMessage(smc, output); err != nil {
		t.Fatalf("postMessage() error = %v", err)
	}
	if requestCount != 1 {
		t.Fatalf("post count = %d, want 1", requestCount)
	}
	if gotThreadTimestamp != "1700000000.000100" {
		t.Fatalf("thread_ts = %q, want root timestamp", gotThreadTimestamp)
	}
	if gotBroadcast != "" {
		t.Fatalf("reply_broadcast = %q, want omitted", gotBroadcast)
	}
}

func TestGetOutputChannelUsesConversationContext(t *testing.T) {
	output := &cmd.CommandOutput{
		ReplyInfo: &slackevents.MessageEvent{Channel: "C-trigger"},
		ConversationContext: cmd.ConversationContext{
			ChannelID: "C-root",
		},
	}

	if got := getOutputChannel(output); got != "C-root" {
		t.Fatalf("output channel = %q", got)
	}
	if got := getReactionChannel(output); got != "C-trigger" {
		t.Fatalf("reaction channel = %q", got)
	}
}

func TestGetOutputChannelFallsBackToTriggeringMessage(t *testing.T) {
	output := &cmd.CommandOutput{ReplyInfo: &slackevents.MessageEvent{Channel: "C-trigger"}}

	if got := getOutputChannel(output); got != "C-trigger" {
		t.Fatalf("output channel = %q", got)
	}
}

func TestGetReactionTimestampUsesTriggeringMessage(t *testing.T) {
	output := &cmd.CommandOutput{
		ReplyInfo: &slackevents.MessageEvent{
			Channel:         "C123",
			TimeStamp:       "1700000000.000200",
			ThreadTimeStamp: "1700000000.000100",
		},
		ConversationContext: cmd.ConversationContext{
			ChannelID:           "C123",
			RootThreadTimestamp: "1700000000.000100",
		},
	}

	if got := getReactionTimestamp(output); got != "1700000000.000200" {
		t.Fatalf("reaction timestamp = %q", got)
	}
}

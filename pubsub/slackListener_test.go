package pubsub

import (
	"encoding/json"
	"testing"

	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
)

func TestNewSlackInputSetsConversationID(t *testing.T) {
	tests := []struct {
		name       string
		message    *slackevents.MessageEvent
		wantRootTS string
	}{
		{
			name:       "root message uses its timestamp",
			message:    &slackevents.MessageEvent{Channel: "C123", TimeStamp: "1700000000.000100"},
			wantRootTS: "1700000000.000100",
		},
		{
			name: "thread reply uses root timestamp",
			message: &slackevents.MessageEvent{
				Channel:         "C123",
				TimeStamp:       "1700000000.000200",
				ThreadTimeStamp: "1700000000.000100",
			},
			wantRootTS: "1700000000.000100",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			input := NewSlackInput(tc.message, "date")
			if input.ConversationID.ChannelID != "C123" {
				t.Fatalf("channel = %q", input.ConversationID.ChannelID)
			}
			if input.ConversationID.RootTimestamp != tc.wantRootTS {
				t.Fatalf(
					"root timestamp = %q, want %q",
					input.ConversationID.RootTimestamp,
					tc.wantRootTS,
				)
			}
			if input.MessageID.ChannelID != tc.message.Channel || input.MessageID.Timestamp != tc.message.TimeStamp {
				t.Fatalf("message ID = %+v", input.MessageID)
			}
		})
	}
}

func TestExtractMessageTextPreservesFallbackOrder(t *testing.T) {
	tests := []struct {
		name  string
		event *slackevents.MessageEvent
		want  string
	}{
		{name: "top level text", event: &slackevents.MessageEvent{Text: "top"}, want: "top"},
		{
			name: "attachment text",
			event: &slackevents.MessageEvent{
				Message: &slack.Msg{Attachments: []slack.Attachment{{Text: "attachment"}}},
			},
			want: "attachment",
		},
		{
			name:  "nested message fallback",
			event: &slackevents.MessageEvent{Message: &slack.Msg{Text: "fallback"}},
			want:  "fallback",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := extractMessageText(tc.event); got != tc.want {
				t.Fatalf("text = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestNewSlackInputFromAppMentionSetsConversationID(t *testing.T) {
	tests := []struct {
		name       string
		message    *slackevents.AppMentionEvent
		wantRootTS string
	}{
		{
			name: "root mention uses its timestamp",
			message: &slackevents.AppMentionEvent{
				Channel:   "C123",
				TimeStamp: "1700000000.000100",
			},
			wantRootTS: "1700000000.000100",
		},
		{
			name: "thread mention uses root timestamp",
			message: &slackevents.AppMentionEvent{
				Channel:         "C123",
				TimeStamp:       "1700000000.000200",
				ThreadTimeStamp: "1700000000.000100",
			},
			wantRootTS: "1700000000.000100",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			input := NewSlackInputFromAppMention(tc.message, "date")
			if input.ConversationID.ChannelID != "C123" {
				t.Fatalf("channel = %q", input.ConversationID.ChannelID)
			}
			if input.ConversationID.RootTimestamp != tc.wantRootTS {
				t.Fatalf(
					"root timestamp = %q, want %q",
					input.ConversationID.RootTimestamp,
					tc.wantRootTS,
				)
			}
			if input.MessageID.ChannelID != tc.message.Channel || input.MessageID.Timestamp != tc.message.TimeStamp {
				t.Fatalf("message ID = %+v", input.MessageID)
			}
		})
	}
}

func TestShouldIgnoreMessageEventPreservesBotAndReminderHandling(t *testing.T) {
	previousUserID := userID
	userID = "U-self"
	t.Cleanup(func() { userID = previousUserID })

	if shouldIgnoreMessageEvent(&slackevents.MessageEvent{SubType: slack.MsgSubTypeBotMessage, User: "U-other"}, Config{AcceptBotMessage: true}) {
		t.Fatal("accepted bot message was ignored")
	}
	if !shouldIgnoreMessageEvent(&slackevents.MessageEvent{SubType: slack.MsgSubTypeBotMessage, User: "U-other"}, Config{}) {
		t.Fatal("bot message ignored accept_bot_message=false was accepted")
	}
	if !shouldIgnoreMessageEvent(&slackevents.MessageEvent{SubType: slack.MsgSubTypeMessageChanged}, Config{}) {
		t.Fatal("message_changed was accepted")
	}
	if !shouldIgnoreMessageEvent(&slackevents.MessageEvent{SubType: slack.MsgSubTypeMessageDeleted}, Config{}) {
		t.Fatal("message_deleted was accepted")
	}
	if shouldIgnoreMessageEvent(&slackevents.MessageEvent{SubType: slack.MsgSubTypeMeMessage}, Config{}) {
		t.Fatal("non-edit subtype was newly ignored")
	}
	if shouldIgnoreMessageEvent(&slackevents.MessageEvent{User: "USLACKBOT", Text: "Reminder: todo"}, Config{AcceptReminder: true}) {
		t.Fatal("accepted reminder was ignored")
	}
}

func TestNormalizeSlackURLs(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "plain URL is unwrapped",
			input: "<https://example.com>",
			want:  "https://example.com",
		},
		{
			name:  "URL with display text returns display text",
			input: "<https://example.com|click here>",
			want:  "click here",
		},
		{
			name:  "URL with empty display text falls back to URL",
			input: "<https://example.com|>",
			want:  "https://example.com",
		},
		{
			name:  "no URL markup is left unchanged",
			input: "hello world",
			want:  "hello world",
		},
		{
			name:  "URL embedded in prose",
			input: "check out <https://example.com|this link> please",
			want:  "check out this link please",
		},
		{
			name:  "multiple URLs in one message",
			input: "<https://a.com> and <https://b.com|B site>",
			want:  "https://a.com and B site",
		},
		{
			name:  "mention pattern is not affected",
			input: "<@U12345>",
			want:  "<@U12345>",
		},
		{
			name:  "special token like <!channel> is not affected",
			input: "<!channel>",
			want:  "<!channel>",
		},
		{
			name:  "http URL is unwrapped",
			input: "<http://example.com>",
			want:  "http://example.com",
		},
		{
			name:  "URL with query string is unwrapped",
			input: "<https://example.com?foo=bar>",
			want:  "https://example.com?foo=bar",
		},
		{
			name:  "empty string is unchanged",
			input: "",
			want:  "",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := normalizeSlackURLs(tc.input)
			if got != tc.want {
				t.Errorf("normalizeSlackURLs(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

func TestExtractEnvelopeID(t *testing.T) {
	tests := []struct {
		name   string
		raw    json.RawMessage
		want   string
		wantOK bool
	}{
		{
			name:   "valid envelope id",
			raw:    json.RawMessage(`{"envelope_id":"abc-123","type":"events_api","payload":{}}`),
			want:   "abc-123",
			wantOK: true,
		},
		{
			name:   "missing envelope id",
			raw:    json.RawMessage(`{"type":"events_api","payload":{}}`),
			want:   "",
			wantOK: false,
		},
		{
			name:   "invalid json",
			raw:    json.RawMessage(`{"envelope_id":`),
			want:   "",
			wantOK: false,
		},
		{
			name:   "empty raw message",
			raw:    json.RawMessage(``),
			want:   "",
			wantOK: false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := extractEnvelopeID(tc.raw)
			if ok != tc.wantOK {
				t.Fatalf("extractEnvelopeID ok = %v, want %v", ok, tc.wantOK)
			}
			if got != tc.want {
				t.Fatalf("extractEnvelopeID = %q, want %q", got, tc.want)
			}
		})
	}
}

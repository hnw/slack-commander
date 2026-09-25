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

func TestPostMessageFormatsAttachments(t *testing.T) {
	tests := []struct {
		name             string
		outputFormat     string
		isErrOut         bool
		wantText         string
		wantMarkdownText string
		wantColor        string
	}{
		{
			name:      "unspecified defaults to plain",
			wantText:  "*output*",
			wantColor: stdoutColor,
		},
		{
			name:         "plain",
			outputFormat: "plain",
			wantText:     "*output*",
			wantColor:    stdoutColor,
		},
		{
			name:         "monospaced",
			outputFormat: "monospaced",
			wantText:     "```*output*```",
			wantColor:    stdoutColor,
		},
		{
			name:             "markdown",
			outputFormat:     "markdown",
			wantMarkdownText: "*output*",
			wantColor:        stdoutColor,
		},
		{
			name:      "stderr uses error color",
			isErrOut:  true,
			wantText:  "*output*",
			wantColor: stderrColor,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var attachments []struct {
				Text   string `json:"text"`
				Color  string `json:"color"`
				Blocks []struct {
					Type string `json:"type"`
					Text string `json:"text"`
				} `json:"blocks"`
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if err := r.ParseForm(); err != nil {
					t.Fatalf("ParseForm() error = %v", err)
				}
				if err := json.Unmarshal([]byte(r.Form.Get("attachments")), &attachments); err != nil {
					t.Fatalf("attachments = %q: %v", r.Form.Get("attachments"), err)
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "channel": "C123", "ts": "1700000000.000300"})
			}))
			defer server.Close()

			smc := socketmode.New(slack.New("token", slack.OptionAPIURL(server.URL+"/")))
			output := &cmd.CommandOutput{
				ReplyInfo:   &slackevents.MessageEvent{Channel: "C123", TimeStamp: "1700000000.000200"},
				ReplyConfig: &ReplyConfig{OutputFormat: tt.outputFormat},
				Text:        "*output*",
				IsErrOut:    tt.isErrOut,
			}

			if err := postMessage(smc, output); err != nil {
				t.Fatalf("postMessage() error = %v", err)
			}
			if len(attachments) != 1 {
				t.Fatalf("attachment count = %d, want 1", len(attachments))
			}
			attachment := attachments[0]
			if attachment.Color != tt.wantColor {
				t.Fatalf("attachment color = %q, want %q", attachment.Color, tt.wantColor)
			}
			if attachment.Text != tt.wantText {
				t.Fatalf("attachment text = %q, want %q", attachment.Text, tt.wantText)
			}
			if tt.wantMarkdownText == "" {
				if len(attachment.Blocks) != 0 {
					t.Fatalf("attachment blocks = %+v, want none", attachment.Blocks)
				}
				return
			}
			if len(attachment.Blocks) != 1 {
				t.Fatalf("markdown block count = %d, want 1", len(attachment.Blocks))
			}
			if block := attachment.Blocks[0]; block.Type != "markdown" || block.Text != tt.wantMarkdownText {
				t.Fatalf("markdown block = %+v, want type markdown with text %q", block, tt.wantMarkdownText)
			}
		})
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

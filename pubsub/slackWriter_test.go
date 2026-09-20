package pubsub

import (
	"testing"

	"github.com/hnw/slack-commander/cmd"
	"github.com/slack-go/slack/slackevents"
)

func TestGetThreadTimestampUsesConversationRoot(t *testing.T) {
	output := &cmd.CommandOutput{
		ReplyInfo: &slackevents.MessageEvent{
			Channel:         "C123",
			TimeStamp:       "1700000000.000200",
			ThreadTimeStamp: "1700000000.000100",
		},
		ReplyConfig: &ReplyConfig{PostAsReply: true},
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
		ReplyConfig: &ReplyConfig{PostAsReply: true},
	}

	if got := getThreadTimestamp(output); got != "1700000000.000200" {
		t.Fatalf("thread timestamp = %q", got)
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

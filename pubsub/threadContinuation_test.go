package pubsub

import (
	"strings"
	"testing"

	"github.com/hnw/slack-commander/cmd"
	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
)

func TestSerializeThreadConversationKeepsCommandLineOutOfStdin(t *testing.T) {
	history := []slack.Message{
		{Msg: slack.Msg{Timestamp: "1", Text: "agent review\nfunc() {helloworld();}"}},
		{Msg: slack.Msg{Timestamp: "2", User: "Ubot", Text: "Please fix it"}},
		{Msg: slack.Msg{Timestamp: "3", User: "Uself", Text: "Fixed\nall tests pass"}},
	}

	got := serializeThreadConversation(
		"agent review\nfunc() {helloworld();}", "1", history, "Uself",
	)
	want := "user: func() {helloworld();}\n\nuser: Please fix it\n\nassistant: Fixed\nall tests pass"
	if got != want {
		t.Fatalf("stdin = %q, want %q", got, want)
	}
}

func TestBuildThreadContinuationInputRequiresCurrentThreadMatch(t *testing.T) {
	history := []slack.Message{
		{Msg: slack.Msg{Timestamp: "1", Text: "agent\ninput"}},
		{Msg: slack.Msg{Timestamp: "2", User: "U123", Text: "reply"}},
	}
	event := &slackevents.MessageEvent{Channel: "C123", TimeStamp: "2", ThreadTimeStamp: "1"}
	thread := []*cmd.CommandConfig{
		cmd.NewCommandConfig(
			&cmd.Definition{
				Keyword: "agent", Command: "agent", Continuation: cmd.ContinuationThread,
			},
			nil,
		),
	}
	plain := []*cmd.CommandConfig{
		cmd.NewCommandConfig(
			&cmd.Definition{Keyword: "agent", Command: "agent"},
			nil,
		),
	}

	if input, ok, err := buildThreadContinuationInput(
		event,
		history,
		Config{},
		thread,
		"Uself",
	); err != nil || !ok ||
		!input.ThreadContinuation {
		t.Fatal("thread root should build a continuation input")
	}
	if _, ok, err := buildThreadContinuationInput(
		event,
		history,
		Config{},
		plain,
		"Uself",
	); err != nil ||
		ok {
		t.Fatal("current non-thread root must not build a continuation input")
	}
}

func TestBuildThreadContinuationInputNormalizesRootAppMention(t *testing.T) {
	history := []slack.Message{
		{Msg: slack.Msg{Timestamp: "1", Text: "<@Uself> agent review"}},
		{Msg: slack.Msg{Timestamp: "2", User: "U123", Text: "reply"}},
	}
	event := &slackevents.MessageEvent{
		Channel:         "C123",
		TimeStamp:       "2",
		ThreadTimeStamp: "1",
	}
	configs := []*cmd.CommandConfig{
		cmd.NewCommandConfig(
			&cmd.Definition{
				Keyword:      "agent *",
				Command:      "agent *",
				Continuation: cmd.ContinuationThread,
			},
			nil,
		),
	}

	input, matched, err := buildThreadContinuationInput(
		event,
		history,
		Config{},
		configs,
		"Uself",
	)
	if err != nil {
		t.Fatalf("buildThreadContinuationInput() error = %v", err)
	}
	if !matched {
		t.Fatal("buildThreadContinuationInput() matched = false, want true")
	}
	if got, want := strings.TrimSpace(input.Text), "agent review\nuser: reply"; got != want {
		t.Fatalf("input.Text = %q, want %q", input.Text, want)
	}
}

func TestBuildThreadContinuationInputKeepsHistoryWithoutChannel(t *testing.T) {
	cfg := Config{
		AllowedChannelIDs: []string{"C123"},
		AllowedUserIDs:    []string{"U123"},
	}
	history := []slack.Message{
		{Msg: slack.Msg{Timestamp: "1", User: "U123", Text: "agent\ninput"}},
		{Msg: slack.Msg{Timestamp: "2", User: "U123", Text: "reply"}},
	}
	event := &slackevents.MessageEvent{
		Channel:         "C123",
		TimeStamp:       "2",
		ThreadTimeStamp: "1",
	}
	if !isAllowedChannel(cfg, event.Channel) {
		t.Fatal("triggering event channel must be allowed")
	}
	configs := []*cmd.CommandConfig{cmd.NewCommandConfig(&cmd.Definition{
		Keyword:      "agent",
		Command:      "agent",
		Continuation: cmd.ContinuationThread,
	}, nil)}

	input, matched, err := buildThreadContinuationInput(event, history, cfg, configs, "Uself")
	if err != nil {
		t.Fatalf("buildThreadContinuationInput() error = %v", err)
	}
	if !matched {
		t.Fatal("buildThreadContinuationInput() matched = false, want true")
	}
	if got, want := input.Text, "agent\nuser: input\n\nuser: reply"; got != want {
		t.Errorf("input.Text = %q, want %q", got, want)
	}
}

func TestHistoryThroughTriggerExcludesLaterMessages(t *testing.T) {
	history := []slack.Message{
		{Msg: slack.Msg{Timestamp: "1", Text: "root"}},
		{Msg: slack.Msg{Timestamp: "2", Text: "trigger"}},
		{Msg: slack.Msg{Timestamp: "3", Text: "later"}},
	}
	got, err := historyThroughTrigger(history, "2")
	if err != nil || len(got) != 2 || got[1].Text != "trigger" {
		t.Fatalf("historyThroughTrigger() = %#v, %v", got, err)
	}
	if _, err := historyThroughTrigger(history, "missing"); err == nil {
		t.Fatal("missing trigger must fail closed")
	}
}

func TestFilterThreadContinuationHistory(t *testing.T) {
	cfg := Config{
		AllowedUserIDs:   []string{"U123", "B123"},
		AcceptBotMessage: true,
	}
	history := []slack.Message{
		{Msg: slack.Msg{Timestamp: "1", User: "Uself"}},
		{Msg: slack.Msg{Timestamp: "2", User: "U123"}},
		{Msg: slack.Msg{Timestamp: "3", User: "U999"}},
		{Msg: slack.Msg{Timestamp: "4", BotID: "B123"}},
	}

	got := filterThreadContinuationHistory(history, cfg, "Uself")

	if len(got) != 3 {
		t.Fatalf("len(filtered) = %d, want 3", len(got))
	}

	want := []string{"1", "2", "4"}
	for i, timestamp := range want {
		if got[i].Timestamp != timestamp {
			t.Fatalf(
				"filtered[%d].Timestamp = %q, want %q",
				i,
				got[i].Timestamp,
				timestamp,
			)
		}
	}
}

func TestFilterThreadContinuationHistoryDropsBotWhenDisabled(t *testing.T) {
	cfg := Config{
		AllowedUserIDs:   []string{"B123"},
		AcceptBotMessage: false,
	}
	history := []slack.Message{
		{Msg: slack.Msg{Timestamp: "1", BotID: "B123"}},
	}

	got := filterThreadContinuationHistory(history, cfg, "Uself")

	if len(got) != 0 {
		t.Fatalf("filtered = %#v, want empty", got)
	}
}

func TestSerializeThreadConversationOmitsOneLineRoot(t *testing.T) {
	got := serializeThreadConversation(
		"agent review",
		"1",
		[]slack.Message{{Msg: slack.Msg{Timestamp: "1", Text: "agent review"}}},
		"Uself",
	)
	if got != "" {
		t.Fatalf("stdin = %q, want empty", got)
	}
}

func TestSerializeThreadConversationUsesAttachmentTextForAssistantOutput(t *testing.T) {
	history := []slack.Message{{Msg: slack.Msg{
		Timestamp: "2",
		User:      "Uself",
		Attachments: []slack.Attachment{{
			Text: "attachment output",
		}},
	}}}

	got := serializeThreadConversation("agent review", "1", history, "Uself")
	if got != "assistant: attachment output" {
		t.Fatalf("stdin = %q", got)
	}
}

func TestBuildThreadContinuationCommandTextUsesOnlyRootFirstLineForCommand(t *testing.T) {
	got := buildThreadContinuationCommandText(
		"agent review\nroot input", "user: root input\n\nuser: reply",
	)
	want := "agent review\nuser: root input\n\nuser: reply"
	if got != want {
		t.Fatalf("command text = %q, want %q", got, want)
	}
}

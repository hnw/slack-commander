package pubsub

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/hnw/slack-commander/cmd"
	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
	"github.com/slack-go/slack/socketmode"
)

func newSlackTestDispatcher(enqueue func(*cmd.CommandInput) bool) *cmd.CommandDispatcher {
	outputs := make(chan *cmd.CommandOutput, 100)
	return cmd.NewCommandDispatcher(context.Background(), cmd.NewExecutor(outputs), outputs, &cmd.StdinStore{}, &cmd.ConversationLocks{}, enqueue)
}

func TestSlackListenerRejectsInputWithoutOwnIdentity(t *testing.T) {
	for _, tc := range []struct {
		name     string
		response string
	}{
		{"auth failure", `{"ok":false,"error":"invalid_auth"}`},
		{"missing user ID", `{"ok":true,"bot_id":"B-self"}`},
		{"missing bot ID", `{"ok":true,"user_id":"U-self"}`},
		{"missing both IDs", `{"ok":true}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			previousUserID, previousBotID := userID, ownBotID
			userID, ownBotID = "U-stale", "B-stale"
			t.Cleanup(func() { userID, ownBotID = previousUserID, previousBotID })
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = io.WriteString(w, tc.response)
			}))
			defer server.Close()
			smc := socketmode.New(slack.New("test", slack.OptionAPIURL(server.URL+"/")))
			smc.Events <- socketmode.Event{Type: socketmode.EventTypeConnected}
			smc.Events <- socketmode.Event{Type: socketmode.EventTypeEventsAPI, Data: slackevents.EventsAPIEvent{
				Type: slackevents.CallbackEvent, InnerEvent: slackevents.EventsAPIInnerEvent{Data: &slackevents.MessageEvent{
					BotID: "B-other", Channel: "C", TimeStamp: "1", Text: "run",
				}},
			}}
			close(smc.Events)
			command := cmd.NewCommand(cmd.CommandConfig{MatcherConfig: cmd.MatcherConfig{RawMatcherConfig: cmd.RawMatcherConfig{Keyword: "run"}}, RunnerConfig: cmd.RunnerConfig{RawRunnerConfig: cmd.RawRunnerConfig{Command: "run"}}}, nil, nil)
			commands := cmd.NewCommandSet([]*cmd.Command{command})
			queued := 0
			router := cmd.NewConversationRouter(nil, commands, nil, newSlackTestDispatcher(func(*cmd.CommandInput) bool {
				queued++
				return true
			}), 1)
			err := SlackListener(context.Background(), smc, Config{ListenerConfigs: []ListenerConfig{{CommandIndex: 0}}}, router)
			if err == nil {
				t.Fatal("SlackListener() accepted incomplete identity")
			}
			if queued != 0 {
				t.Fatalf("queued %d inputs without own identity", queued)
			}
		})
	}
}

func TestSlackListenerIdentifiesOwnPostsBeforeAcceptingInput(t *testing.T) {
	previousUserID, previousBotID := userID, ownBotID
	userID, ownBotID = "U-stale", "B-stale"
	t.Cleanup(func() { userID, ownBotID = previousUserID, previousBotID })
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"ok":true,"user_id":"U-self","bot_id":"B-self"}`)
	}))
	defer server.Close()
	smc := socketmode.New(slack.New("test", slack.OptionAPIURL(server.URL+"/")))
	for _, event := range []*slackevents.MessageEvent{
		{User: "U-self", Channel: "C", TimeStamp: "1", Text: "run"},
		{BotID: "B-self", Channel: "C", TimeStamp: "2", Text: "run"},
		{User: "U-other", Channel: "C", TimeStamp: "3", Text: "run"},
		{BotID: "B-other", Channel: "C", TimeStamp: "4", Text: "run"},
	} {
		smc.Events <- socketmode.Event{Type: socketmode.EventTypeEventsAPI, Data: slackevents.EventsAPIEvent{
			Type: slackevents.CallbackEvent, InnerEvent: slackevents.EventsAPIInnerEvent{Data: event},
		}}
	}
	close(smc.Events)
	command := cmd.NewCommand(cmd.CommandConfig{MatcherConfig: cmd.MatcherConfig{RawMatcherConfig: cmd.RawMatcherConfig{Keyword: "run"}}, RunnerConfig: cmd.RunnerConfig{RawRunnerConfig: cmd.RawRunnerConfig{Command: "run"}}}, nil, nil)
	var queued []string
	commands := cmd.NewCommandSet([]*cmd.Command{command})
	router := cmd.NewConversationRouter(nil, commands, nil, newSlackTestDispatcher(func(input *cmd.CommandInput) bool {
		queued = append(queued, input.MessageID.Timestamp)
		return true
	}), 1)
	if err := SlackListener(context.Background(), smc, Config{ListenerConfigs: []ListenerConfig{{CommandIndex: 0}}}, router); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(queued, []string{"3", "4"}) {
		t.Fatalf("queued timestamps = %v, want only other senders", queued)
	}
}

func TestListenerSkipsEventsWithoutCommandCandidates(t *testing.T) {
	previousUserID, previousBotID := userID, ownBotID
	userID, ownBotID = "", ""
	t.Cleanup(func() { userID, ownBotID = previousUserID, previousBotID })

	reply := cmd.NewCommand(cmd.CommandConfig{Index: 1, MatcherConfig: cmd.MatcherConfig{RawMatcherConfig: cmd.RawMatcherConfig{Keyword: "retry"}}, RunnerConfig: cmd.RunnerConfig{RawRunnerConfig: cmd.RawRunnerConfig{Command: "retry"}}}, nil, nil)
	root := cmd.NewCommand(cmd.CommandConfig{Index: 0, MatcherConfig: cmd.MatcherConfig{RawMatcherConfig: cmd.RawMatcherConfig{Keyword: "run"}}, RunnerConfig: cmd.RunnerConfig{RawRunnerConfig: cmd.RawRunnerConfig{Command: "run"}}}, nil, cmd.NewCommandSet([]*cmd.Command{reply}))
	commands := cmd.NewCommandSet([]*cmd.Command{root})
	resolverCalls, enqueueCalls := 0, 0
	router := cmd.NewConversationRouter(nil, commands, func(cmd.ConversationID) (cmd.RootCommandInput, error) {
		resolverCalls++
		return cmd.RootCommandInput{Text: "run", AllowedCommandIndexes: []int{0}}, nil
	}, newSlackTestDispatcher(func(*cmd.CommandInput) bool {
		enqueueCalls++
		return true
	}), 1)

	for _, tc := range []struct {
		name string
		call func(*cmd.ConversationRouter)
	}{
		{name: "root message", call: func(router *cmd.ConversationRouter) {
			onMessageEvent(nil, &slackevents.MessageEvent{User: "U", Channel: "C", TimeStamp: "1", Text: "run"}, Config{}, router)
		}},
		{name: "thread reply", call: func(router *cmd.ConversationRouter) {
			onMessageEvent(nil, &slackevents.MessageEvent{User: "U", Channel: "C", TimeStamp: "2", ThreadTimeStamp: "1", Text: "retry"}, Config{}, router)
		}},
		{name: "root app mention", call: func(router *cmd.ConversationRouter) {
			onAppMentionEvent(nil, &slackevents.AppMentionEvent{User: "U", Channel: "C", TimeStamp: "1", Text: "<@BOT> run"}, Config{}, router)
		}},
		{name: "thread app mention", call: func(router *cmd.ConversationRouter) {
			onAppMentionEvent(nil, &slackevents.AppMentionEvent{User: "U", Channel: "C", TimeStamp: "2", ThreadTimeStamp: "1", Text: "<@BOT> retry"}, Config{}, router)
		}},
	} {
		t.Run(tc.name, func(*testing.T) {
			tc.call(router)
		})
	}
	if resolverCalls != 0 || enqueueCalls != 0 {
		t.Fatalf("resolver calls = %d, enqueue calls = %d, want both zero", resolverCalls, enqueueCalls)
	}
}

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

func TestShouldIgnoreMessageEventIgnoresOwnBotAndKeepsOtherSenders(t *testing.T) {
	previousUserID := userID
	previousBotID := ownBotID
	userID = "U-self"
	ownBotID = "B-self"
	t.Cleanup(func() { userID, ownBotID = previousUserID, previousBotID })

	if shouldIgnoreMessageEvent(&slackevents.MessageEvent{SubType: slack.MsgSubTypeBotMessage, User: "U-other", BotID: "B-other"}) {
		t.Fatal("other bot message was ignored")
	}
	if !shouldIgnoreMessageEvent(&slackevents.MessageEvent{User: "U-self"}) {
		t.Fatal("own user message was accepted")
	}
	if !shouldIgnoreMessageEvent(&slackevents.MessageEvent{BotID: "B-self"}) {
		t.Fatal("own bot message without user ID was accepted")
	}
	userID, ownBotID = "", ""
	if isOwnBotMessage("", "") {
		t.Fatal("empty unknown sender matched an unset own ID")
	}
	userID, ownBotID = "U-self", "B-self"
	if !shouldIgnoreMessageEvent(&slackevents.MessageEvent{SubType: slack.MsgSubTypeMessageChanged}) {
		t.Fatal("message_changed was accepted")
	}
	if !shouldIgnoreMessageEvent(&slackevents.MessageEvent{SubType: slack.MsgSubTypeMessageDeleted}) {
		t.Fatal("message_deleted was accepted")
	}
	if shouldIgnoreMessageEvent(&slackevents.MessageEvent{SubType: slack.MsgSubTypeMeMessage}) {
		t.Fatal("non-edit subtype was newly ignored")
	}
}

func TestFilterCommandIndexesUsesReminderExceptionAndBotSenderAllowlist(t *testing.T) {
	configs := []ListenerConfig{
		{CommandIndex: 0, RawListenerConfig: RawListenerConfig{AllowedUserIDs: []string{"U-normal"}, AllowedChannelIDs: []string{"C-main"}}},
		{CommandIndex: 1, RawListenerConfig: RawListenerConfig{AllowedUserIDs: []string{"U-admin"}, AllowedChannelIDs: []string{"C-main"}, AcceptReminder: true}},
		{CommandIndex: 2, RawListenerConfig: RawListenerConfig{AllowedUserIDs: []string{}, AllowedChannelIDs: []string{"C-ops"}, AcceptReminder: true}},
		{CommandIndex: 3, RawListenerConfig: RawListenerConfig{AllowedUserIDs: []string{"B-other"}, AllowedChannelIDs: []string{"C-main"}}},
		{CommandIndex: 4, IsReply: true, RawListenerConfig: RawListenerConfig{AllowedUserIDs: []string{"U-normal"}, AllowedChannelIDs: []string{"C-main"}}},
	}
	tests := []struct {
		name     string
		user     string
		channel  string
		reminder bool
		want     []int
	}{
		{name: "user and channel restrict ordinary message", user: "U-normal", channel: "C-main", want: []int{0}},
		{name: "reminder skips user allowlist only", user: "USLACKBOT", channel: "C-main", reminder: true, want: []int{1}},
		{name: "reminder still respects channel allowlist", user: "USLACKBOT", channel: "C-ops", reminder: true, want: []int{2}},
		{name: "non-reminder slackbot sender is allowlisted normally", user: "USLACKBOT", channel: "C-main", want: []int{}},
		{name: "bot sender ID is allowlisted normally", user: senderIDForEvent("U-other", "B-other"), channel: "C-main", want: []int{3}},
		{name: "empty result is explicit", user: "U-other", channel: "C-other", want: []int{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := filterCommandIndexes(configs, tt.user, tt.channel, tt.reminder, false); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("filterCommandIndexes() = %v, want %v", got, tt.want)
			}
		})
	}
	if got := filterCommandIndexes(configs, "U-normal", "C-main", false, true); !reflect.DeepEqual(got, []int{4}) {
		t.Fatalf("reply candidates = %v, want only reply index", got)
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

package pubsub

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"regexp"
	"strings"

	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
	"github.com/slack-go/slack/socketmode"

	"github.com/hnw/slack-commander/cmd"
)

var (
	userID          string // bot自身のuser ID（注：bot IDではない）
	reMentionTarget = regexp.MustCompile(`<@[^>]+>`)
	reSlackURL      = regexp.MustCompile(`<([^@!|>\s][^|>]*)(?:\|([^>]*))?>`)
)

// NewSlackInput はSlackの入力を元にpubsub.Inputを返す
func NewSlackInput(msg *slackevents.MessageEvent, text string) *cmd.CommandInput {
	return &cmd.CommandInput{
		ConversationID: newConversationID(
			msg.Channel,
			msg.TimeStamp,
			msg.ThreadTimeStamp,
		),
		MessageID: cmd.MessageID{ChannelID: msg.Channel, Timestamp: msg.TimeStamp},
		Text:      normalizeSlackText(text),
	}
}

// NewSlackInputFromAppMention はAppMentionEventを元にpubsub.Inputを返す
func NewSlackInputFromAppMention(msg *slackevents.AppMentionEvent, text string) *cmd.CommandInput {
	return &cmd.CommandInput{
		ConversationID: newConversationID(
			msg.Channel,
			msg.TimeStamp,
			msg.ThreadTimeStamp,
		),
		MessageID: cmd.MessageID{ChannelID: msg.Channel, Timestamp: msg.TimeStamp},
		Text:      normalizeSlackText(text),
	}
}

func newConversationID(channelID, timestamp, threadTimestamp string) cmd.ConversationID {
	rootTimestamp := timestamp
	if threadTimestamp != "" {
		rootTimestamp = threadTimestamp
	}
	return cmd.ConversationID{
		ChannelID:     channelID,
		RootTimestamp: rootTimestamp,
	}
}

// SlackListener watches Socket Mode events and routes new messages to commands.
func SlackListener(
	ctx context.Context,
	smc *socketmode.Client,
	cfg Config,
	coordinator *cmd.ConversationCoordinator,
) {
	for {
		select {
		case <-ctx.Done():
			return
		case evt, ok := <-smc.Events:
			if !ok {
				return
			}
			ackSocketModeEvent(smc, evt)

			switch evt.Type {
			case socketmode.EventTypeConnecting:
				smc.Debugf("[INFO] Connecting to Slack with Socket Mode...")
			case socketmode.EventTypeConnectionError:
				smc.Debugf("[INFO] Connection failed. Retrying later...")
			case socketmode.EventTypeConnected:
				smc.Debugf("[INFO] Connected to Slack with Socket Mode.")

				authTest, authTestErr := smc.AuthTest()
				if authTestErr != nil {
					smc.Debugf(
						"[WARN] AuthTest() failed. Continue without bot user ID: %v",
						authTestErr,
					)
					continue
				}
				userID = authTest.UserID
			case socketmode.EventTypeEventsAPI:
				eventsAPIEvent, ok := evt.Data.(slackevents.EventsAPIEvent)
				if !ok {
					smc.Debugf("[INFO] Ignored %+v\n", evt)
					continue
				}
				switch eventsAPIEvent.Type {
				case slackevents.CallbackEvent:
					innerEvent := eventsAPIEvent.InnerEvent
					switch ev := innerEvent.Data.(type) {
					case *slackevents.MessageEvent:
						onMessageEvent(smc, ev, cfg, coordinator)
					case *slackevents.AppMentionEvent:
						onAppMentionEvent(smc, ev, cfg, coordinator)
					default:
						smc.Debugf("[INFO] Unsupported inner event type: %v", ev)
					}
				default:
					smc.Debugf("[INFO] Unsupported Events API event received")
				}

			default:
				smc.Debugf("[INFO] Unexpected event type received: %s\n", evt.Type)
			}
		}
	}
}

func ackSocketModeEvent(smc *socketmode.Client, evt socketmode.Event) {
	if evt.Request != nil && evt.Request.EnvelopeID != "" {
		if err := smc.Ack(*evt.Request); err != nil {
			smc.Debugf("[WARN] failed to ack envelope_id=%s: %v", evt.Request.EnvelopeID, err)
		}
		return
	}
	if evt.Type != socketmode.EventTypeErrorBadMessage {
		return
	}
	errEvt, ok := evt.Data.(*socketmode.ErrorBadMessage)
	if !ok || errEvt == nil {
		return
	}
	envelopeID, ok := extractEnvelopeID(errEvt.Message)
	if !ok {
		smc.Debugf("[WARN] error_bad_message without envelope_id; cannot ack")
		return
	}
	if err := smc.Ack(socketmode.Request{EnvelopeID: envelopeID}); err != nil {
		smc.Debugf("[WARN] failed to ack envelope_id=%s: %v", envelopeID, err)
		return
	}
	smc.Debugf("[WARN] acked error_bad_message envelope_id=%s", envelopeID)
}

func extractEnvelopeID(raw json.RawMessage) (string, bool) {
	if len(raw) == 0 {
		return "", false
	}
	var req socketmode.Request
	if err := json.Unmarshal(raw, &req); err != nil {
		return "", false
	}
	if req.EnvelopeID == "" {
		return "", false
	}
	return req.EnvelopeID, true
}

func shouldIgnoreMessageEvent(ev *slackevents.MessageEvent, cfg Config) bool {
	switch ev.SubType {
	case slack.MsgSubTypeMessageChanged, slack.MsgSubTypeMessageDeleted:
		return true
	}
	if ev.User == "USLACKBOT" && !cfg.AcceptReminder {
		return true
	}
	if ev.SubType == "bot_message" && (ev.User == userID || !cfg.AcceptBotMessage) {
		// botからのメッセージを無視する & AcceptBotMessageがtrueでも自身からのメッセージは無視する
		return true
	}
	return false
}

func shouldIgnoreAppMentionEvent(ev *slackevents.AppMentionEvent, cfg Config) bool {
	if ev.User == "USLACKBOT" && !cfg.AcceptReminder {
		return true
	}
	if ev.BotID != "" && (ev.User == userID || !cfg.AcceptBotMessage) {
		// botからのメッセージを無視する & AcceptBotMessageがtrueでも自身からのメッセージは無視する
		return true
	}
	return false
}

func senderIDForEvent(user, botID string) string {
	if botID != "" {
		return botID
	}
	return user
}

func extractReminderText(user, text string) (string, bool) {
	if user != "USLACKBOT" || !strings.HasPrefix(text, "Reminder: ") {
		return "", false
	}
	trimmed := strings.TrimPrefix(text, "Reminder: ")
	trimmed = strings.TrimSuffix(trimmed, ".")
	return trimmed, true
}

func extractMessageText(ev *slackevents.MessageEvent) string {
	if text, ok := extractReminderText(ev.User, ev.Text); ok {
		return text
	}
	if ev.Message == nil {
		return ev.Text
	}
	if text := slackMessageText(ev.Text, ev.Message.Attachments); text != "" {
		return text
	}
	return ev.Message.Text
}

func extractAppMentionText(ev *slackevents.AppMentionEvent) string {
	if text, ok := extractReminderText(ev.User, ev.Text); ok {
		return text
	}
	return ev.Text
}

func slackMessageText(text string, attachments []slack.Attachment) string {
	if text != "" {
		return text
	}
	return attachmentTextValue(attachments)
}

func rootMessageText(root *slack.Message) string {
	text := slackMessageText(root.Text, root.Attachments)
	if reminderText, ok := extractReminderText(root.User, text); ok {
		return reminderText
	}
	return text
}

func attachmentTextValue(attachments []slack.Attachment) string {
	if len(attachments) == 0 {
		return ""
	}
	attachment := attachments[0]
	if attachment.Pretext != "" {
		text := attachment.Pretext
		if attachment.Text != "" {
			text = text + "\n" + attachment.Text
		}
		return text
	}
	if attachment.Text != "" {
		return attachment.Text
	}
	return ""
}

// normalizeSlackText converts Slack-specific text into command text at the input boundary.
func normalizeSlackText(text string) string {
	text = removeMentionTarget(text)
	text = normalizeSlackURLs(text)
	text = normalizeQuotes(unescapeMessage(text))
	return text
}

func onMessageEvent(
	smc *socketmode.Client,
	ev *slackevents.MessageEvent,
	cfg Config,
	coordinator *cmd.ConversationCoordinator,
) {
	if shouldIgnoreMessageEvent(ev, cfg) {
		return
	}
	senderID := senderIDForEvent(ev.User, ev.BotID)
	if !isAllowedUser(cfg, senderID) || !isAllowedChannel(cfg, ev.Channel) {
		return
	}
	input := NewSlackInput(ev, extractMessageText(ev))
	if ev.ThreadTimeStamp != "" {
		routeThreadReply(smc, input, coordinator)
		return
	}
	if input.Text == "" {
		return
	}
	if coordinator == nil {
		smc.Debugf("[WARN] conversation coordinator is unavailable; dropping message event command")
		return
	}
	if !coordinator.AcceptRoot(input) {
		smc.Debugf("[WARN] command queue is full; dropping message event command")
		return
	}
	smc.Debugf("[DEBUG]: command = '%s'", input.Text)
}

func onAppMentionEvent(
	smc *socketmode.Client,
	ev *slackevents.AppMentionEvent,
	cfg Config,
	coordinator *cmd.ConversationCoordinator,
) {
	if shouldIgnoreAppMentionEvent(ev, cfg) {
		return
	}
	senderID := senderIDForEvent(ev.User, ev.BotID)
	if !isAllowedUser(cfg, senderID) || !isAllowedChannel(cfg, ev.Channel) {
		return
	}
	input := NewSlackInputFromAppMention(ev, extractAppMentionText(ev))
	if ev.ThreadTimeStamp != "" {
		routeThreadReply(smc, input, coordinator)
		return
	}
	if input.Text == "" {
		return
	}
	if coordinator == nil {
		smc.Debugf("[WARN] conversation coordinator is unavailable; dropping app_mention command")
		return
	}
	if !coordinator.AcceptRoot(input) {
		smc.Debugf("[WARN] command queue is full; dropping app_mention command")
		return
	}
	smc.Debugf("[DEBUG]: command = '%s'", input.Text)
}

func routeThreadReply(
	smc *socketmode.Client,
	input *cmd.CommandInput,
	coordinator *cmd.ConversationCoordinator,
) {
	if input.Text == "" || smc == nil || coordinator == nil {
		return
	}
	result, err := coordinator.AcceptThreadReply(input)
	if err != nil {
		log.Printf("[WARN] unable to fetch thread root channel=%s thread=%s: %v", input.ConversationID.ChannelID, input.ConversationID.RootTimestamp, err)
		return
	}
	if result == cmd.ThreadReplyQueueFull {
		smc.Debugf("[WARN] command queue is full; dropping thread reply command")
	}
}

func getThreadRoot(smc *socketmode.Client, conversation cmd.ConversationID) (*slack.Message, error) {
	messages, _, _, err := smc.GetConversationReplies(&slack.GetConversationRepliesParameters{
		ChannelID: conversation.ChannelID,
		Timestamp: conversation.RootTimestamp,
		Inclusive: true,
		Limit:     1,
	})
	if err != nil {
		return nil, err
	}
	for i := range messages {
		if messages[i].Timestamp == conversation.RootTimestamp {
			return &messages[i], nil
		}
	}
	return nil, fmt.Errorf("thread root not found")
}

// SlackRootTextResolver fetches a thread root and applies Slack-specific text normalization.
func SlackRootTextResolver(smc *socketmode.Client) cmd.RootTextResolver {
	return func(conversation cmd.ConversationID) (string, error) {
		root, err := getThreadRoot(smc, conversation)
		if err != nil {
			return "", err
		}
		return normalizeSlackText(rootMessageText(root)), nil
	}
}

// remove mention target from message text (like <@USLACKBOT>)
func removeMentionTarget(message string) string {
	return reMentionTarget.ReplaceAllString(message, "")
}

// normalizeSlackURLs replaces Slack URL markup with plain text.
// <url> becomes url, and <url|text> becomes text (or url if text is empty).
func normalizeSlackURLs(message string) string {
	return reSlackURL.ReplaceAllStringFunc(message, func(match string) string {
		submatches := reSlackURL.FindStringSubmatch(match)
		if len(submatches) < 3 {
			return match
		}
		url, displayText := submatches[1], submatches[2]
		if displayText != "" {
			return displayText
		}
		return url
	})
}

// unescapeMessage
// Unescape HTML entities
func unescapeMessage(message string) string {
	replacer := strings.NewReplacer("&amp;", "&", "&lt;", "<", "&gt;", ">")
	return replacer.Replace(message)
}

// normalizeQuotes
// Replace all quotes in message with standard ascii quotes
func normalizeQuotes(message string) string {
	// U+2018 LEFT SINGLE QUOTATION MARK
	// U+2019 RIGHT SINGLE QUOTATION MARK
	// U+201C LEFT DOUBLE QUOTATION MARK
	// U+201D RIGHT DOUBLE QUOTATION MARK
	replacer := strings.NewReplacer(`‘`, `'`, `’`, `'`, `“`, `"`, `”`, `"`)
	return replacer.Replace(message)
}

func isAllowedUser(cfg Config, userID string) bool {
	if len(cfg.AllowedUserIDs) == 0 {
		return true
	}
	for _, allowed := range cfg.AllowedUserIDs {
		if userID == allowed {
			return true
		}
	}
	return false
}

func isAllowedChannel(cfg Config, channelID string) bool {
	if len(cfg.AllowedChannelIDs) == 0 {
		return true
	}
	for _, allowed := range cfg.AllowedChannelIDs {
		if channelID == allowed {
			return true
		}
	}
	return false
}

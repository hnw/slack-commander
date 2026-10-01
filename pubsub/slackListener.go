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
	ownBotID        string
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
) error {
	if err := identifyOwnBot(ctx, smc); err != nil {
		return err
	}

	for {
		select {
		case <-ctx.Done():
			return nil
		case evt, ok := <-smc.Events:
			if !ok {
				return nil
			}
			ackSocketModeEvent(smc, evt)

			switch evt.Type {
			case socketmode.EventTypeConnecting:
				smc.Debugf("[INFO] Connecting to Slack with Socket Mode...")
			case socketmode.EventTypeConnectionError:
				smc.Debugf("[INFO] Connection failed. Retrying later...")
			case socketmode.EventTypeConnected:
				smc.Debugf("[INFO] Connected to Slack with Socket Mode.")
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

func identifyOwnBot(ctx context.Context, smc *socketmode.Client) error {
	authTest, err := smc.AuthTestContext(ctx)
	if err != nil {
		return fmt.Errorf("slack listener auth.test: %w", err)
	}
	if authTest.UserID == "" || authTest.BotID == "" {
		return fmt.Errorf("slack listener auth.test: UserID and BotID are required")
	}
	userID, ownBotID = authTest.UserID, authTest.BotID
	return nil
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

func shouldIgnoreMessageEvent(ev *slackevents.MessageEvent) bool {
	switch ev.SubType {
	case slack.MsgSubTypeMessageChanged, slack.MsgSubTypeMessageDeleted:
		return true
	}
	return isOwnBotMessage(ev.User, ev.BotID)
}

func shouldIgnoreAppMentionEvent(ev *slackevents.AppMentionEvent) bool {
	return isOwnBotMessage(ev.User, ev.BotID)
}

func isOwnBotMessage(user, botID string) bool {
	return (userID != "" && user == userID) || (ownBotID != "" && botID == ownBotID)
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

func isReminderMessage(user, text string) bool {
	_, ok := extractReminderText(user, text)
	return ok
}

func extractMessageText(ev *slackevents.MessageEvent) string {
	rawText := messageEventText(ev)
	if text, ok := extractReminderText(ev.User, rawText); ok {
		return text
	}
	if ev.Message == nil {
		return rawText
	}
	if text := slackMessageText(rawText, ev.Message.Attachments); text != "" {
		return text
	}
	return ev.Message.Text
}

func messageEventText(ev *slackevents.MessageEvent) string {
	if ev.Text != "" || ev.Message == nil {
		return ev.Text
	}
	return slackMessageText(ev.Message.Text, ev.Message.Attachments)
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
	if shouldIgnoreMessageEvent(ev) {
		return
	}
	senderID := senderIDForEvent(ev.User, ev.BotID)
	input := NewSlackInput(ev, extractMessageText(ev))
	isReply := input.MessageID.Timestamp != input.ConversationID.RootTimestamp
	input.AllowedCommandIndexes = filterCommandIndexes(cfg.ListenerConfigs, senderID, ev.Channel, isReminderMessage(ev.User, messageEventText(ev)), isReply)
	if len(input.AllowedCommandIndexes) == 0 || input.Text == "" {
		return
	}
	if coordinator == nil {
		smc.Debugf("[WARN] conversation coordinator is unavailable; dropping message event command")
		return
	}
	result, err := coordinator.Accept(input)
	if err != nil {
		log.Printf("[WARN] unable to fetch thread root channel=%s thread=%s: %v", input.ConversationID.ChannelID, input.ConversationID.RootTimestamp, err)
		return
	}
	if result == cmd.AcceptQueueFull {
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
	if shouldIgnoreAppMentionEvent(ev) {
		return
	}
	senderID := senderIDForEvent(ev.User, ev.BotID)
	input := NewSlackInputFromAppMention(ev, extractAppMentionText(ev))
	isReply := input.MessageID.Timestamp != input.ConversationID.RootTimestamp
	input.AllowedCommandIndexes = filterCommandIndexes(cfg.ListenerConfigs, senderID, ev.Channel, isReminderMessage(ev.User, ev.Text), isReply)
	if len(input.AllowedCommandIndexes) == 0 || input.Text == "" {
		return
	}
	if coordinator == nil {
		smc.Debugf("[WARN] conversation coordinator is unavailable; dropping app_mention command")
		return
	}
	result, err := coordinator.Accept(input)
	if err != nil {
		log.Printf("[WARN] unable to fetch thread root channel=%s thread=%s: %v", input.ConversationID.ChannelID, input.ConversationID.RootTimestamp, err)
		return
	}
	if result == cmd.AcceptQueueFull {
		smc.Debugf("[WARN] command queue is full; dropping app_mention command")
		return
	}
	smc.Debugf("[DEBUG]: command = '%s'", input.Text)
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

// SlackRootInputResolver はcache eviction後も起点投稿者のACLでrouteを再判定する。
func SlackRootInputResolver(smc *socketmode.Client, cfg Config) cmd.RootInputResolver {
	return func(conversation cmd.ConversationID) (cmd.RootCommandInput, error) {
		root, err := getThreadRoot(smc, conversation)
		if err != nil {
			return cmd.RootCommandInput{}, err
		}
		reminder := isReminderMessage(root.User, slackMessageText(root.Text, root.Attachments))
		senderID := senderIDForEvent(root.User, root.BotID)
		indexes := filterCommandIndexes(cfg.ListenerConfigs, senderID, conversation.ChannelID, reminder, false)
		if isOwnBotMessage(root.User, root.BotID) {
			indexes = []int{}
		}
		return cmd.RootCommandInput{Text: normalizeSlackText(rootMessageText(root)), AllowedCommandIndexes: indexes}, nil
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

func isAllowedID(allowedIDs []string, id string) bool {
	if len(allowedIDs) == 0 {
		return true
	}
	for _, allowed := range allowedIDs {
		if id == allowed {
			return true
		}
	}
	return false
}

func filterCommandIndexes(configs []ListenerConfig, userID, channelID string, reminder, reply bool) []int {
	allowed := make([]int, 0, len(configs))
	for _, config := range configs {
		if config.IsReply != reply {
			continue
		}
		if reminder && !config.AcceptReminder {
			continue
		}
		if !reminder && !isAllowedID(config.AllowedUserIDs, userID) {
			continue
		}
		if !isAllowedID(config.AllowedChannelIDs, channelID) {
			continue
		}
		allowed = append(allowed, config.CommandIndex)
	}
	return allowed
}

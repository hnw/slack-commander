package pubsub

import (
	"fmt"
	"strings"

	"github.com/slack-go/slack"
)

func historyThroughTrigger(
	history []slack.Message,
	triggerTimestamp string,
) ([]slack.Message, error) {
	for i, message := range history {
		if message.Timestamp == triggerTimestamp {
			return history[:i+1], nil
		}
	}
	return nil, fmt.Errorf("triggering message %q is missing from thread history", triggerTimestamp)
}

func filterThreadContinuationHistory(
	history []slack.Message,
	cfg Config,
	commanderUserID string,
) []slack.Message {
	filtered := make([]slack.Message, 0, len(history))
	for _, message := range history {
		if message.User == commanderUserID {
			filtered = append(filtered, message)
			continue
		}
		if message.BotID != "" && !cfg.AcceptBotMessage {
			continue
		}
		if !isAllowedUser(cfg, senderIDForEvent(message.User, message.BotID)) {
			continue
		}
		filtered = append(filtered, message)
	}
	return filtered
}

func serializeThreadConversation(
	rootText, rootTimestamp string,
	history []slack.Message,
	commanderUserID string,
) string {
	entries := make([]string, 0, len(history)+1)
	normalizedRoot := normalizeCommandText(rootText)
	if _, rootStdin, ok := strings.Cut(normalizedRoot, "\n"); ok && rootStdin != "" {
		entries = append(entries, "user: "+rootStdin)
	}
	for _, message := range history {
		text := slackMessageText(message.Text, message.Attachments)
		if message.Timestamp == rootTimestamp || text == "" {
			continue
		}
		role := "user"
		if message.User == commanderUserID {
			role = "assistant"
		} else {
			text = normalizeCommandText(text)
		}
		entries = append(entries, role+": "+text)
	}
	return strings.Join(entries, "\n\n")
}

func buildThreadContinuationCommandText(rootText, stdin string) string {
	commandLine, _, _ := strings.Cut(normalizeCommandText(rootText), "\n")
	if stdin == "" {
		return commandLine
	}
	return commandLine + "\n" + stdin
}

package main

import (
	"strings"

	"github.com/slack-go/slack"
)

func slackListener(rtm *slack.RTM, commandQueue chan *slackInput) {
	for msg := range rtm.IncomingEvents {
		switch ev := msg.Data.(type) {
		case *slack.HelloEvent:
			logger.Println("[DEBUG] Hello event")

		case *slack.ConnectedEvent:
			logger.Println("[DEBUG] Infos:", ev.Info)
			logger.Println("[INFO] Connection counter:", ev.ConnectionCount)

		case *slack.MessageEvent:
			logger.Printf("[DEBUG] Message: %v, text=%s\n", ev, ev.Text)
			onMessageEvent(rtm, ev, commandQueue)

		case *slack.RTMError:
			logger.Printf("[INFO] Error: %s\n", ev.Error())

		case *slack.InvalidAuthEvent:
			logger.Println("[INFO] Invalid credentials")
			return

		default:
			// Ignore other events..
			//fmt.Printf("[DEBUG] Unexpected: %v\n", msg.Data)
		}
	}
}

func onMessageEvent(rtm *slack.RTM, ev *slack.MessageEvent, commandQueue chan *slackInput) {
	if ev.User == "USLACKBOT" && config.AcceptReminder == false {
		return
	}
	if ev.SubType == "bot_message" && config.AcceptBotMessage == false {
		return
	}
	if ev.ThreadTimestamp != "" && config.AcceptThreadMessage == false {
		return
	}
	if ev.User == "USLACKBOT" && strings.HasPrefix(ev.Text, "Reminder: ") {
		text := strings.TrimPrefix(ev.Text, "Reminder: ")
		text = strings.TrimSuffix(text, ".")
		commandQueue <- newSlackInput(ev, text)
	} else if ev.Text != "" {
		commandQueue <- newSlackInput(ev, UnescapeMessage(ev.Text))
	} else if ev.Attachments != nil {
		if ev.Attachments[0].Pretext != "" {
			// attachmentのpretextとtextを文字列連結してtext扱いにする
			text := ev.Attachments[0].Pretext
			if ev.Attachments[0].Text != "" {
				text = text + "\n" + ev.Attachments[0].Text
			}
			commandQueue <- newSlackInput(ev, text)
		} else if ev.Attachments[0].Text != "" {
			commandQueue <- newSlackInput(ev, ev.Attachments[0].Text)
		}
	}
}

// UnescapeMessage text
func UnescapeMessage(message string) string {
	replacer := strings.NewReplacer("&amp;", "&", "&lt;", "<", "&gt;", ">")
	return replacer.Replace(message)
}

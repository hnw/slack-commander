package main

import (
	"strings"

	"github.com/slack-go/slack"
)

var (
	botID string
)

func slackListener(rtm *slack.RTM, commandQueue chan *slackInput) {
	for msg := range rtm.IncomingEvents {
		switch ev := msg.Data.(type) {
		case *slack.HelloEvent:
			logger.Println("[DEBUG] Hello event")

		case *slack.ConnectedEvent:
			logger.Println("[DEBUG] Infos:", ev.Info)
			logger.Println("[INFO] Connection counter:", ev.ConnectionCount)
			if botID == "" {
				// 自身のBotIDを取得するのにAPIアクセスが必要
				botUser, err := rtm.Client.GetUserInfo(ev.Info.User.ID)
				if err != nil {
					logger.Println("[INFO] GetUserInfo() failed.")
					return
				}
				botID = botUser.Profile.BotID
			}

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
	if ev.SubType == "bot_message" &&
		(ev.BotID == botID || config.AcceptBotMessage == false) {
		// 自身のコメントで無限ループするのを防ぐ。
		// SubType == "bot_message" のときev.Userは空文字列になりUser IDでチェックできない
		// そのためBot IDでチェックする必要がある
		return
	}
	if ev.ThreadTimestamp != "" && config.AcceptThreadMessage == false {
		return
	}
	if ev.User == "USLACKBOT" && strings.HasPrefix(ev.Text, "Reminder: ") {
		text := strings.TrimPrefix(ev.Text, "Reminder: ")
		text = strings.TrimSuffix(text, ".")
		commandQueue <- newSlackInput(ev, NormalizeQuotes(UnescapeMessage(text)))
	} else if ev.Text != "" {
		commandQueue <- newSlackInput(ev, NormalizeQuotes(UnescapeMessage(ev.Text)))
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

// NormalizeQuotes
// Replace all quotes in message with standard ascii quotes
func NormalizeQuotes(message string) string {
	// U+2018 LEFT SINGLE QUOTATION MARK
	// U+2019 RIGHT SINGLE QUOTATION MARK
	// U+201C LEFT DOUBLE QUOTATION MARK
	// U+201D RIGHT DOUBLE QUOTATION MARK
	replacer := strings.NewReplacer(`‘`, `'`, `’`, `'`, `“`, `"`, `”`, `"`)
	return replacer.Replace(message)
}

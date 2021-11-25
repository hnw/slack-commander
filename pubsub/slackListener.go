package pubsub

import (
	"fmt"
	"strings"

	"github.com/slack-go/slack/slackevents"
	"github.com/slack-go/slack/socketmode"

	"github.com/hnw/slack-commander/cmd"
)

var (
	userID string // bot自身のuser ID（注：bot IDではない）
)

// NewSlackInput はSlackの入力を元にpubsub.Inputを返す
func NewSlackInput(msg *slackevents.MessageEvent, text string) *cmd.CommandInput {
	return &cmd.CommandInput{
		ReplyInfo: msg,
		Text:      text,
	}
}

// SlackListener はSocket Modeでメッセージ監視し、コマンドをcommandQueueに投げます。
func SlackListener(smc *socketmode.Client, commandQueue chan *cmd.CommandInput, cfg Config) {
	for evt := range smc.Events {
		switch evt.Type {
		case socketmode.EventTypeConnecting:
			smc.Debugf("[INFO] Connecting to Slack with Socket Mode...")
		case socketmode.EventTypeConnectionError:
			smc.Debugf("[INFO] Connection failed. Retrying later...")
		case socketmode.EventTypeConnected:
			smc.Debugf("[INFO] Connected to Slack with Socket Mode.")

			authTest, authTestErr := smc.AuthTest()
			if authTestErr != nil {
				smc.Debugf("[ERROR] AuthTest() failed. : %v", authTestErr)
				panic(fmt.Sprintf("AuthTest() failed. : %v", authTestErr))
			}
			userID = authTest.UserID
		case socketmode.EventTypeEventsAPI:
			eventsAPIEvent, ok := evt.Data.(slackevents.EventsAPIEvent)
			if !ok {
				smc.Debugf("[INFO] Ignored %+v\n", evt)
				continue
			}
			smc.Ack(*evt.Request)

			switch eventsAPIEvent.Type {
			case slackevents.CallbackEvent:
				innerEvent := eventsAPIEvent.InnerEvent
				switch ev := innerEvent.Data.(type) {
				case *slackevents.MessageEvent:
					onMessageEvent(smc, ev, commandQueue, cfg)
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

func onMessageEvent(smc *socketmode.Client, ev *slackevents.MessageEvent, commandQueue chan *cmd.CommandInput, cfg Config) {
	if ev.User == "USLACKBOT" && cfg.AcceptReminder == false {
		return
	}
	if ev.SubType == "bot_message" &&
		(ev.User == userID || cfg.AcceptBotMessage == false) {
		// AcceptBotMessageがtrueでも自身からのメッセージは無視する（直前のブロックを除く）。
		// SubType == "bot_message" のときev.Userは空文字列になりUser IDでチェックできない
		// そのためBot IDでチェックする必要がある
		return
	}
	if ev.ThreadTimeStamp != "" && cfg.AcceptThreadMessage == false {
		return
	}
	if ev.User == "USLACKBOT" && strings.HasPrefix(ev.Text, "Reminder: ") {
		text := strings.TrimPrefix(ev.Text, "Reminder: ")
		text = strings.TrimSuffix(text, ".")
		commandQueue <- NewSlackInput(ev, normalizeQuotes(unescapeMessage(text)))
		smc.Debugf("[DEBUG]: command = '%s'", normalizeQuotes(unescapeMessage(text)))
	} else if ev.Text != "" {
		commandQueue <- NewSlackInput(ev, normalizeQuotes(unescapeMessage(ev.Text)))
		smc.Debugf("[DEBUG]: command = '%s'", normalizeQuotes(unescapeMessage(ev.Text)))
	} else if ev.Attachments != nil {
		// おそらくsocket modeではこの分岐に入らない、確認してあとで消す
		if ev.Attachments[0].Pretext != "" {
			// attachmentのpretextとtextを文字列連結してtext扱いにする
			text := normalizeQuotes(unescapeMessage(ev.Attachments[0].Pretext))
			if ev.Attachments[0].Text != "" {
				text = text + "\n" + ev.Attachments[0].Text
			}
			commandQueue <- NewSlackInput(ev, text)
			smc.Debugf("[DEBUG]: command = '%s'", text)
		} else if ev.Attachments[0].Text != "" {
			commandQueue <- NewSlackInput(ev, ev.Attachments[0].Text)
			smc.Debugf("[DEBUG]: command = '%s'", ev.Attachments[0].Text)
		}
	}
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

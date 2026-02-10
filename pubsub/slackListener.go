package pubsub

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/slack-go/slack/slackevents"
	"github.com/slack-go/slack/socketmode"

	"github.com/hnw/slack-commander/cmd"
)

var (
	userID          string // bot自身のuser ID（注：bot IDではない）
	reMentionTarget = regexp.MustCompile(`<@[^>]+>`)
)

// NewSlackInput はSlackの入力を元にpubsub.Inputを返す
func NewSlackInput(msg *slackevents.MessageEvent, text string) *cmd.CommandInput {
	return &cmd.CommandInput{
		ReplyInfo: msg,
		Text:      text,
	}
}

// NewSlackInputFromAppMention はAppMentionEventを元にpubsub.Inputを返す
func NewSlackInputFromAppMention(msg *slackevents.AppMentionEvent, text string) *cmd.CommandInput {
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
				case *slackevents.AppMentionEvent:
					onAppMentionEvent(smc, ev, commandQueue, cfg)
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
		// botからのメッセージを無視する & AcceptBotMessageがtrueでも自身からのメッセージは無視する
		return
	}
	if ev.ThreadTimeStamp != "" && cfg.AcceptThreadMessage == false {
		return
	}
	text := ""
	if ev.User == "USLACKBOT" && strings.HasPrefix(ev.Text, "Reminder: ") {
		text = strings.TrimPrefix(ev.Text, "Reminder: ")
		text = strings.TrimSuffix(text, ".")
	} else if ev.Text != "" {
		text = ev.Text
	} else if ev.Message != nil && ev.Message.Attachments != nil {
		if ev.Message.Attachments[0].Pretext != "" {
			// attachmentのpretextとtextを文字列連結してtext扱いにする
			text = ev.Message.Attachments[0].Pretext
			if ev.Message.Attachments[0].Text != "" {
				text = text + "\n" + ev.Message.Attachments[0].Text
			}
		} else if ev.Message.Attachments[0].Text != "" {
			text = ev.Message.Attachments[0].Text
		} else {
			smc.Debugf("[DEBUG]: text(4) = ''")
		}
	} else if ev.Message != nil && ev.Message.Text != "" {
		text = ev.Message.Text
	}
	text = removeMentionTarget(text)
	text = normalizeQuotes(unescapeMessage(text))
	if text != "" {
		commandQueue <- NewSlackInput(ev, text)
		smc.Debugf("[DEBUG]: command = '%s'", text)
	}
}

func onAppMentionEvent(smc *socketmode.Client, ev *slackevents.AppMentionEvent, commandQueue chan *cmd.CommandInput, cfg Config) {
	if ev.User == "USLACKBOT" && cfg.AcceptReminder == false {
		return
	}
	if ev.BotID != "" &&
		(ev.User == userID || cfg.AcceptBotMessage == false) {
		// botからのメッセージを無視する & AcceptBotMessageがtrueでも自身からのメッセージは無視する
		return
	}
	if ev.ThreadTimeStamp != "" && cfg.AcceptThreadMessage == false {
		return
	}
	text := ""
	if ev.User == "USLACKBOT" && strings.HasPrefix(ev.Text, "Reminder: ") {
		text = strings.TrimPrefix(ev.Text, "Reminder: ")
		text = strings.TrimSuffix(text, ".")
	} else if ev.Text != "" {
		text = ev.Text
	}
	text = removeMentionTarget(text)
	text = normalizeQuotes(unescapeMessage(text))
	if text != "" {
		commandQueue <- NewSlackInputFromAppMention(ev, text)
		smc.Debugf("[DEBUG]: command = '%s'", text)
	}
}

// remove mention target from message text (like <@USLACKBOT>)
func removeMentionTarget(message string) string {
	return reMentionTarget.ReplaceAllString(message, "")
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

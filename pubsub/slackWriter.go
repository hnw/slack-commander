package pubsub

import (
	"fmt"
	"time"

	"github.com/slack-go/slack"
)

// SlackWriter はoutputQueueから来たコマンド実行結果をSlackに書き込みます
func SlackWriter(rtm *slack.RTM, outputQueue chan *CommandOutput) {
	for {
		output, ok := <-outputQueue // closeされると ok が false になる
		if !ok {
			return
		}
		postMessage(rtm, output)
		time.Sleep(1 * time.Second)
	}
}

func postMessage(rtm *slack.RTM, output *CommandOutput) error {
	params := slack.PostMessageParameters{
		Username:        output.Username,
		IconEmoji:       output.IconEmoji,
		IconURL:         output.IconURL,
		ThreadTimestamp: output.getThreadTimestamp(),
		ReplyBroadcast:  output.getReplyBroadcast(),
	}
	attachment := slack.Attachment{
		Text:  output.getText(),
		Color: output.getColor(),
	}
	msgOptParams := slack.MsgOptionPostMessageParameters(params)
	msgOptAttachment := slack.MsgOptionAttachments(attachment)
	origMsg := output.ReplyInfo.(*slack.MessageEvent)
	if _, _, err := rtm.PostMessage(origMsg.Channel, msgOptParams, msgOptAttachment); err != nil {
		fmt.Printf("%s\n", err)
		return err
	}
	return nil
}

func (output *CommandOutput) getThreadTimestamp() string {
	if output.PostAsReply {
		origMsg := output.ReplyInfo.(*slack.MessageEvent)
		return origMsg.Timestamp
	}
	return ""
}

func (output *CommandOutput) getReplyBroadcast() bool {
	if output.PostAsReply == false {
		return false
	}
	if output.AlwaysBroadcast {
		return true
	}
	if output.IsError {
		return true
	}
	return false
}

func (output *CommandOutput) getText() string {
	text := output.Text
	if output.Monospaced {
		text = fmt.Sprintf("```%s```", text)
	}
	return text
}

func (output *CommandOutput) getColor() string {
	if output.IsError {
		return "danger"
	}
	return "good"
}

package main

import (
	"fmt"
	"time"

	"github.com/slack-go/slack"
)

func slackWriter(rtm *slack.RTM, writeQueue chan *commandOutput) {
	for {
		output, ok := <-writeQueue // closeされると ok が false になる
		if !ok {
			return
		}
		postMessage(rtm, output)
		time.Sleep(1 * time.Second)
	}
}

func postMessage(rtm *slack.RTM, output *commandOutput) error {
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
	if _, _, err := rtm.PostMessage(output.origMessage.Channel, msgOptParams, msgOptAttachment); err != nil {
		fmt.Printf("%s\n", err)
		return err
	}
	return nil
}

func (output *commandOutput) getThreadTimestamp() string {
	if output.PostAsReply {
		return output.origMessage.Timestamp
	}
	return ""
}

func (output *commandOutput) getReplyBroadcast() bool {
	if output.PostAsReply == false {
		return false
	}
	if output.AlwaysBroadcast {
		return true
	}
	if output.isError {
		return true
	}
	return false
}

func (output *commandOutput) getText() string {
	text := output.text
	if output.Monospaced {
		text = fmt.Sprintf("```%s```", text)
	}
	return text
}

func (output *commandOutput) getColor() string {
	if output.isError {
		return "danger"
	}
	return "good"
}

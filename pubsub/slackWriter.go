package pubsub

import (
	"fmt"
	"time"

	"github.com/slack-go/slack"

	"github.com/hnw/slack-commander/cmd"
)

// SlackWriter はoutputQueueから来たコマンド実行結果をSlackに書き込みます
func SlackWriter(rtm *slack.RTM, outputQueue chan *cmd.CommandOutput) {
	for {
		output, ok := <-outputQueue // closeされると ok が false になる
		if !ok {
			return
		}
		postMessage(rtm, output)
		time.Sleep(1 * time.Second)
	}
}

func postMessage(rtm *slack.RTM, output *cmd.CommandOutput) error {
	cfg := getConfig(output)
	params := slack.PostMessageParameters{
		Username:        cfg.Username,
		IconEmoji:       cfg.IconEmoji,
		IconURL:         cfg.IconURL,
		ThreadTimestamp: getThreadTimestamp(output),
		ReplyBroadcast:  getReplyBroadcast(output),
	}
	attachment := slack.Attachment{
		Text:  getText(output),
		Color: getColor(output),
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

func getConfig(output *cmd.CommandOutput) *ReplyConfig {
	if output.ReplyConfig == nil {
		// TODO: 設定できるようにする
		return &ReplyConfig{
			Username:  "Slack commander",
			IconEmoji: ":ghost:",
		}
	}
	return output.ReplyConfig.(*ReplyConfig)
}

func getThreadTimestamp(output *cmd.CommandOutput) string {
	cfg := getConfig(output)
	if cfg.PostAsReply {
		origMsg := output.ReplyInfo.(*slack.MessageEvent)
		return origMsg.Timestamp
	}
	return ""
}

func getText(output *cmd.CommandOutput) string {
	cfg := getConfig(output)
	text := output.Text
	if cfg.Monospaced {
		text = fmt.Sprintf("```%s```", text)
	}
	return text
}

func getReplyBroadcast(output *cmd.CommandOutput) bool {
	cfg := getConfig(output)
	if cfg.PostAsReply == false {
		return false
	}
	if cfg.AlwaysBroadcast {
		return true
	}
	if output.IsError {
		return true
	}
	return false
}

func getColor(output *cmd.CommandOutput) string {
	if output.IsError {
		return "danger"
	}
	return "good"
}

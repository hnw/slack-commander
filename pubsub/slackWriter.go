package pubsub

import (
	"fmt"
	"time"

	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
	"github.com/slack-go/slack/socketmode"

	"github.com/hnw/slack-commander/cmd"
)

// SlackWriter はoutputQueueから来たコマンド実行結果をSlackに書き込みます
func SlackWriter(smc *socketmode.Client, outputQueue chan *cmd.CommandOutput) {
	runningProcess := 0
	for {
		output, ok := <-outputQueue // closeされると ok が false になる
		if !ok {
			return
		}
		if output.Spawned {
			runningProcess++
			addReaction(smc, output, "eyes")
		} else if output.Finished {
			runningProcess--
			if output.ExitCode == 0 {
				addReaction(smc, output, "white_check_mark")
			} else {
				addReaction(smc, output, "x")
			}
			removeReaction(smc, output, "eyes")
		}
		if output.Text != "" {
			postMessage(smc, output)
		}
		time.Sleep(1 * time.Second)
	}
}

func addReaction(smc *socketmode.Client, output *cmd.CommandOutput, name string) error {
	origMsg := output.ReplyInfo.(*slackevents.MessageEvent)
	item := slack.NewRefToMessage(origMsg.Channel, origMsg.TimeStamp)
	return smc.AddReaction(name, item)
}

func removeReaction(smc *socketmode.Client, output *cmd.CommandOutput, name string) error {
	origMsg := output.ReplyInfo.(*slackevents.MessageEvent)
	item := slack.NewRefToMessage(origMsg.Channel, origMsg.TimeStamp)
	return smc.RemoveReaction(name, item)
}

func postMessage(smc *socketmode.Client, output *cmd.CommandOutput) error {
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
	origMsg := output.ReplyInfo.(*slackevents.MessageEvent)
	if _, _, err := smc.PostMessage(origMsg.Channel, msgOptParams, msgOptAttachment); err != nil {
		smc.Debugf("[ERROR] %s\n", err)
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
		origMsg := output.ReplyInfo.(*slackevents.MessageEvent)
		return origMsg.TimeStamp
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
	if output.IsErrOut {
		return true
	}
	return false
}

func getColor(output *cmd.CommandOutput) string {
	if output.IsErrOut {
		return "danger"
	}
	return "good"
}

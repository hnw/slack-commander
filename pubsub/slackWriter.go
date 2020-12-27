package pubsub

import (
	"fmt"
	"time"

	"github.com/slack-go/slack"

	"github.com/hnw/slack-commander/cmd"
)

// SlackWriter はoutputQueueから来たコマンド実行結果をSlackに書き込みます
func SlackWriter(rtm *slack.RTM, outputQueue chan *cmd.CommandOutput) {
	runningProcess := 0
	if err := rtm.SetUserPresence("away"); err != nil {
		fmt.Printf("err = %v\n", err)
	}
	for {
		output, ok := <-outputQueue // closeされると ok が false になる
		if !ok {
			return
		}
		if output.Spawned {
			runningProcess++
			if runningProcess == 1 {
				if err := rtm.SetUserPresence("auto"); err != nil {
					fmt.Printf("err = %v\n", err)
				}
			}
			addReaction(rtm, output, "eyes")
		} else if output.Finished {
			runningProcess--
			if runningProcess == 0 {
				if err := rtm.SetUserPresence("away"); err != nil {
					fmt.Printf("err = %v\n", err)
				}
			}
			if output.ExitCode == 0 {
				addReaction(rtm, output, "white_check_mark")
			} else {
				addReaction(rtm, output, "x")
			}
			removeReaction(rtm, output, "eyes")
		}
		if output.Text != "" {
			postMessage(rtm, output)
		}
		time.Sleep(1 * time.Second)
	}
}

func addReaction(rtm *slack.RTM, output *cmd.CommandOutput, name string) error {
	origMsg := output.ReplyInfo.(*slack.MessageEvent)
	item := slack.NewRefToMessage(origMsg.Channel, origMsg.Timestamp)
	return rtm.AddReaction(name, item)
}

func removeReaction(rtm *slack.RTM, output *cmd.CommandOutput, name string) error {
	origMsg := output.ReplyInfo.(*slack.MessageEvent)
	item := slack.NewRefToMessage(origMsg.Channel, origMsg.Timestamp)
	return rtm.RemoveReaction(name, item)
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

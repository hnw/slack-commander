package pubsub

import (
	"bytes"
	"fmt"
	"strings"
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
		if hasMeaningfulText(output) {
			postMessage(smc, output)
		}
		if output.ImageData != nil {
			if err := uploadImage(smc, output); err != nil {
				smc.Debugf("[ERROR] uploadImage: %s\n", err)
			}
		}
	}
}

func addReaction(smc *socketmode.Client, output *cmd.CommandOutput, name string) error {
	ch := getChannel(output)
	ts := getTimeStamp(output)
	item := slack.NewRefToMessage(ch, ts)
	return smc.AddReaction(name, item)
}

func removeReaction(smc *socketmode.Client, output *cmd.CommandOutput, name string) error {
	ch := getChannel(output)
	ts := getTimeStamp(output)
	item := slack.NewRefToMessage(ch, ts)
	return smc.RemoveReaction(name, item)
}

func postMessage(smc *socketmode.Client, output *cmd.CommandOutput) error {
	if !hasMeaningfulText(output) {
		return nil
	}
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
	ch := getChannel(output)
	if _, _, err := smc.PostMessage(ch, msgOptParams, msgOptAttachment); err != nil {
		smc.Debugf("[ERROR] %s\n", err)
		return err
	}
	return nil
}

func postMessageWithImageBlock(
	smc *socketmode.Client,
	output *cmd.CommandOutput,
	fileID string,
) error {
	cfg := getConfig(output)
	params := slack.PostMessageParameters{
		Username:        cfg.Username,
		IconEmoji:       cfg.IconEmoji,
		IconURL:         cfg.IconURL,
		ThreadTimestamp: getThreadTimestamp(output),
		ReplyBroadcast:  getReplyBroadcast(output),
	}
	msgOpts := []slack.MsgOption{slack.MsgOptionPostMessageParameters(params)}

	blocks := []slack.Block{}
	if hasMeaningfulText(output) {
		textObj := slack.NewTextBlockObject("mrkdwn", getText(output), false, false)
		blocks = append(blocks, slack.NewSectionBlock(textObj, nil, nil))
	}
	altText := "image output"
	blocks = append(
		blocks,
		slack.NewImageBlockSlackFile(&slack.SlackFileObject{ID: fileID}, altText, "", nil),
	)
	msgOpts = append(msgOpts, slack.MsgOptionBlocks(blocks...))

	if hasMeaningfulText(output) {
		msgOpts = append(msgOpts, slack.MsgOptionText(getText(output), false))
	}

	ch := getChannel(output)
	if _, _, err := smc.PostMessage(ch, msgOpts...); err != nil {
		return err
	}
	return nil
}

func uploadImage(smc *socketmode.Client, output *cmd.CommandOutput) error {
	cfg := getConfig(output)
	params := slack.UploadFileParameters{
		Reader:   bytes.NewReader(output.ImageData),
		FileSize: len(output.ImageData),
		Filename: "output.png",
		Title:    cfg.Username + " output",
	}
	fileSummary, err := smc.UploadFile(params)
	if err != nil {
		return err
	}
	if fileSummary == nil || fileSummary.ID == "" {
		return fmt.Errorf("uploadImage: missing file ID")
	}
	var lastErr error
	delay := 200 * time.Millisecond
	for attempt := 1; attempt <= 5; attempt++ {
		if attempt > 1 {
			time.Sleep(delay)
			delay *= 2
		}
		if err := postMessageWithImageBlock(smc, output, fileSummary.ID); err != nil {
			lastErr = err
			if isInvalidBlocks(err) {
				smc.Debugf("[WARN] uploadImage: invalid_blocks (attempt %d/5)\n", attempt)
				continue
			}
			return err
		}
		return nil
	}
	return lastErr
}

func isInvalidBlocks(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "invalid_blocks")
}

func hasMeaningfulText(output *cmd.CommandOutput) bool {
	return strings.TrimSpace(output.Text) != ""
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
		return getTimeStamp(output)
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

func getChannel(output *cmd.CommandOutput) string {
	switch origMsg := output.ReplyInfo.(type) {
	case *slackevents.MessageEvent:
		return origMsg.Channel
	case *slackevents.AppMentionEvent:
		return origMsg.Channel
	default:
		panic("cast failed")
	}
}

func getTimeStamp(output *cmd.CommandOutput) string {
	switch origMsg := output.ReplyInfo.(type) {
	case *slackevents.MessageEvent:
		return origMsg.TimeStamp
	case *slackevents.AppMentionEvent:
		return origMsg.TimeStamp
	default:
		panic("cast failed")
	}
}

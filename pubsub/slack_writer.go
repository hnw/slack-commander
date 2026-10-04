package pubsub

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/slack-go/slack"
	"github.com/slack-go/slack/socketmode"
)

// SlackWriter はoutputQueueから来たコマンド実行結果をSlackに書き込みます
func SlackWriter(ctx context.Context, smc *socketmode.Client, outputQueue chan *CommandOutput) {
	runningProcess := 0
	for {
		select {
		case output, ok := <-outputQueue: // closeされると ok が false になる
			if !ok {
				return
			}
			runningProcess = handleOutput(smc, output, runningProcess)
		case <-ctx.Done():
			for output := range outputQueue {
				runningProcess = handleOutput(smc, output, runningProcess)
			}
			return
		}
	}
}

func handleOutput(smc *socketmode.Client, output *CommandOutput, runningProcess int) int {
	if output.Spawned {
		runningProcess++
		if err := addReaction(smc, output, "eyes"); err != nil {
			smc.Debugf("[ERROR] addReaction: %s\n", err)
		}
	} else if output.Finished {
		runningProcess--
		if output.ExitCode == 0 {
			if err := addReaction(smc, output, "white_check_mark"); err != nil {
				smc.Debugf("[ERROR] addReaction: %s\n", err)
			}
		} else {
			if err := addReaction(smc, output, "x"); err != nil {
				smc.Debugf("[ERROR] addReaction: %s\n", err)
			}
		}
		if err := removeReaction(smc, output, "eyes"); err != nil {
			smc.Debugf("[ERROR] removeReaction: %s\n", err)
		}
	}
	if hasMeaningfulText(output) {
		if err := postMessage(smc, output); err != nil {
			smc.Debugf("[ERROR] postMessage: %s\n", err)
		}
	}
	if output.ImageData != nil {
		if err := uploadImage(smc, output); err != nil {
			smc.Debugf("[ERROR] uploadImage: %s\n", err)
		}
	}
	return runningProcess
}

func addReaction(smc *socketmode.Client, output *CommandOutput, name string) error {
	ch := getReactionChannel(output)
	ts := getReactionTimestamp(output)
	item := slack.NewRefToMessage(ch, ts)
	return smc.AddReaction(name, item)
}

func removeReaction(smc *socketmode.Client, output *CommandOutput, name string) error {
	ch := getReactionChannel(output)
	ts := getReactionTimestamp(output)
	item := slack.NewRefToMessage(ch, ts)
	return smc.RemoveReaction(name, item)
}

func postMessage(smc *socketmode.Client, output *CommandOutput) error {
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
	attachment := buildTextAttachment(output)
	msgOptParams := slack.MsgOptionPostMessageParameters(params)
	msgOptAttachment := slack.MsgOptionAttachments(attachment)
	ch := getOutputChannel(output)
	if _, _, err := smc.PostMessage(ch, msgOptParams, msgOptAttachment); err != nil {
		smc.Debugf("[ERROR] %s\n", err)
		return err
	}
	return nil
}

func postMessageWithImageBlock(
	smc *socketmode.Client,
	output *CommandOutput,
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
		blocks = append(blocks, buildTextBlock(output))
	}
	altText := "image output"
	blocks = append(
		blocks,
		slack.NewImageBlockSlackFile(&slack.SlackFileObject{ID: fileID}, altText, "", nil),
	)
	msgOpts = append(msgOpts, slack.MsgOptionBlocks(blocks...))

	if hasMeaningfulText(output) {
		msgOpts = append(msgOpts, slack.MsgOptionText(output.Text, false))
	}

	ch := getOutputChannel(output)
	if _, _, err := smc.PostMessage(ch, msgOpts...); err != nil {
		return err
	}
	return nil
}

func uploadImage(smc *socketmode.Client, output *CommandOutput) error {
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

func hasMeaningfulText(output *CommandOutput) bool {
	return strings.TrimSpace(output.Text) != ""
}

func getConfig(output *CommandOutput) *ReplyConfig {
	if output.ReplyConfig == nil {
		return NewSystemReplyConfig(nil)
	}
	return output.ReplyConfig
}

func getThreadTimestamp(output *CommandOutput) string {
	return output.ConversationID.RootTimestamp
}

func getReactionTimestamp(output *CommandOutput) string {
	return output.MessageID.Timestamp
}

func buildTextAttachment(output *CommandOutput) slack.Attachment {
	attachment := slack.Attachment{Color: getColor(output)}
	switch getConfig(output).OutputFormat {
	case OutputFormatMonospaced:
		attachment.Text = fmt.Sprintf("```%s```", output.Text)
	case OutputFormatMarkdown:
		attachment.Blocks = slack.Blocks{
			BlockSet: []slack.Block{slack.NewMarkdownBlock("", output.Text)},
		}
	default:
		attachment.Text = output.Text
	}
	return attachment
}

func buildTextBlock(output *CommandOutput) slack.Block {
	switch getConfig(output).OutputFormat {
	case OutputFormatMarkdown:
		return slack.NewMarkdownBlock("", output.Text)
	case OutputFormatMonospaced:
		textObj := slack.NewTextBlockObject("mrkdwn", fmt.Sprintf("```%s```", output.Text), false, false)
		return slack.NewSectionBlock(textObj, nil, nil)
	default:
		textObj := slack.NewTextBlockObject("mrkdwn", output.Text, false, false)
		return slack.NewSectionBlock(textObj, nil, nil)
	}
}

func getReplyBroadcast(output *CommandOutput) bool {
	cfg := getConfig(output)
	if cfg.ReplyBroadcast == nil {
		return true
	}
	return *cfg.ReplyBroadcast
}

const (
	stdoutColor = "#2EB67D"
	stderrColor = "#E01E5A"
)

func getColor(output *CommandOutput) string {
	if output.IsErrOut {
		return stderrColor
	}
	return stdoutColor
}

func getOutputChannel(output *CommandOutput) string {
	return output.ConversationID.ChannelID
}

func getReactionChannel(output *CommandOutput) string {
	return output.MessageID.ChannelID
}

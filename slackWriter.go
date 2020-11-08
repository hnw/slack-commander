package main

import (
	"bytes"
	"fmt"
	"time"

	"github.com/slack-go/slack"
)

type slackBuffer struct {
	buffer bytes.Buffer
	queue  *(chan *commandInfo)
	info   *commandInfo
	timer  *time.Timer
}

func slackWriter(rtm *slack.RTM, writeQueue chan *commandInfo) {
	for {
		info, ok := <-writeQueue // closeされると ok が false になる
		if !ok {
			return
		}
		if info.Output != "" {
			info.postMessage(rtm)
		}
		time.Sleep(1 * time.Second)
	}
}

func (info *commandInfo) postMessage(rtm *slack.RTM) error {
	params := slack.PostMessageParameters{
		Username:        info.Config.Username,
		IconEmoji:       info.Config.IconEmoji,
		IconURL:         info.Config.IconURL,
		ThreadTimestamp: info.getThreadTimestamp(),
		ReplyBroadcast:  info.getReplyBroadcast(),
	}
	attachment := slack.Attachment{
		Text:  info.getText(),
		Color: info.getColor(),
	}
	msgOptParams := slack.MsgOptionPostMessageParameters(params)
	msgOptAttachment := slack.MsgOptionAttachments(attachment)
	if _, _, err := rtm.PostMessage(info.Message.Channel, msgOptParams, msgOptAttachment); err != nil {
		fmt.Printf("%s\n", err)
		return err
	}
	return nil
}

func (info *commandInfo) getThreadTimestamp() string {
	if info.Config.PostAsReply {
		return info.Message.Timestamp
	}
	return ""
}

func (info *commandInfo) getReplyBroadcast() bool {
	if info.Config.PostAsReply == false {
		return false
	}
	if info.Config.AlwaysBroadcast {
		return true
	}
	if info.ErrorOccurred {
		return true
	}
	return false
}

func (info *commandInfo) getText() string {
	text := info.Output
	if info.Config.Monospaced {
		text = fmt.Sprintf("```%s```", text)
	}
	return text
}

func (info *commandInfo) getColor() string {
	if info.ErrorOccurred {
		return "danger"
	}
	return "good"
}

func (b *slackBuffer) Write(data []byte) (n int, err error) {
	//fmt.Printf("len=%d\n", len(data))
	if b.timer != nil {
		b.timer.Stop()
	}
	l := b.buffer.Len() + len(data)
	i := 0
	for l > 2000 {
		writeSize := 2000
		//fmt.Printf("====================\n")
		if b.buffer.Len() > 0 {
			//fmt.Printf("%s", b.buffer.Bytes())
			writeSize -= b.buffer.Len()
			b.buffer.Truncate(0)
		}
		hunk := data[i : i+writeSize]
		b.info.Output = fmt.Sprintf("%s%s", b.buffer.Bytes(), hunk)
		tmp := *(b.info)
		*(b.queue) <- &tmp
		i += writeSize
		l -= 2000
	}
	n, err = b.buffer.Write(data[i:])
	// 最後に出力されてから3秒間何も出力されなければflashする
	b.timer = time.AfterFunc(3*time.Second, func() {
		b.timer.Stop()
		b.Flash()
	})
	n += i //今回のWriteで書き込まれた総バイト数
	return
}

func (b *slackBuffer) Flash() {
	b.info.Output = fmt.Sprintf("%s", b.buffer.Bytes())
	b.buffer.Truncate(0)
	tmp := *(b.info)
	*(b.queue) <- &tmp
}

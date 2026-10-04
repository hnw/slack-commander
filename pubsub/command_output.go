package pubsub

import (
	"io"
	"time"

	"github.com/hnw/slack-commander/cmd"
)

type slackOutputEvent struct {
	ReplyConfig    *ReplyConfig
	ConversationID cmd.ConversationID
	MessageID      cmd.MessageID
	Text           string // コマンドからのテキスト出力（ImageData と排他）
	ImageData      []byte // sixel を変換した PNG バイト列（Text と排他）
	IsErrOut       bool
	Spawned        bool
	Finished       bool
	ExitCode       int
}

type slackCommandOutput struct {
	queue             chan<- *slackOutputEvent
	replyConfig       *ReplyConfig
	systemReplyConfig *ReplyConfig
	flushInterval     time.Duration
}

var _ cmd.CommandOutput = (*slackCommandOutput)(nil)

func (h *slackCommandOutput) Stdout(c cmd.ConversationID, m cmd.MessageID) cmd.OutputStream {
	return newStdWriter(h.queue, h.replyConfig, c, m, h.flushInterval)
}

func (h *slackCommandOutput) Stderr(c cmd.ConversationID, m cmd.MessageID) cmd.OutputStream {
	return newErrWriter(h.queue, h.replyConfig, c, m, h.flushInterval)
}

func (h *slackCommandOutput) Start(c cmd.ConversationID, m cmd.MessageID) {
	h.queue <- &slackOutputEvent{ConversationID: c, MessageID: m, Spawned: true}
}

func (h *slackCommandOutput) Finish(c cmd.ConversationID, m cmd.MessageID, code int) {
	h.queue <- &slackOutputEvent{ConversationID: c, MessageID: m, Finished: true, ExitCode: code}
}

func (h *slackCommandOutput) SystemError(c cmd.ConversationID, m cmd.MessageID, text string, kind cmd.SystemErrorKind) {
	switch kind {
	case cmd.SystemErrorParse:
		h.queue <- &slackOutputEvent{ConversationID: c, MessageID: m, ReplyConfig: h.systemReplyConfig, Text: text, IsErrOut: true, ExitCode: 2}
	case cmd.SystemErrorCommandNotFound:
		stream := newErrWriter(h.queue, nil, c, m, DefaultOutputFlushInterval)
		_, _ = io.WriteString(stream, text)
		_ = stream.Flush()
	}
}

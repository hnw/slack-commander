package messaging

import (
	"io"
	"time"

	"github.com/hnw/slack-commander/internal/command"
)

type slackOutputEvent struct {
	ReplyConfig    *ReplyConfig
	ConversationID command.ConversationID
	MessageID      command.MessageID
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

var _ command.CommandOutput = (*slackCommandOutput)(nil)

func (h *slackCommandOutput) Stdout(c command.ConversationID, m command.MessageID) command.OutputStream {
	return newStdWriter(h.queue, h.replyConfig, c, m, h.flushInterval)
}

func (h *slackCommandOutput) Stderr(c command.ConversationID, m command.MessageID) command.OutputStream {
	return newErrWriter(h.queue, h.replyConfig, c, m, h.flushInterval)
}

func (h *slackCommandOutput) Start(c command.ConversationID, m command.MessageID) {
	h.queue <- &slackOutputEvent{ConversationID: c, MessageID: m, Spawned: true}
}

func (h *slackCommandOutput) Finish(c command.ConversationID, m command.MessageID, code int) {
	h.queue <- &slackOutputEvent{ConversationID: c, MessageID: m, Finished: true, ExitCode: code}
}

func (h *slackCommandOutput) SystemError(c command.ConversationID, m command.MessageID, text string, kind command.SystemErrorKind) {
	switch kind {
	case command.SystemErrorParse:
		h.queue <- &slackOutputEvent{ConversationID: c, MessageID: m, ReplyConfig: h.systemReplyConfig, Text: text, IsErrOut: true, ExitCode: 2}
	case command.SystemErrorCommandNotFound:
		stream := newErrWriter(h.queue, nil, c, m, DefaultOutputFlushInterval)
		_, _ = io.WriteString(stream, text)
		_ = stream.Flush()
	}
}

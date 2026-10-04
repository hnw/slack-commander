package pubsub

import (
	"io"
	"time"

	"github.com/hnw/slack-commander/cmd"
)

// CommandOutput carries execution output and input errors through the output queue.
type CommandOutput struct {
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

type slackOutputHandler struct {
	queue             chan *CommandOutput
	replyConfig       *ReplyConfig
	systemReplyConfig *ReplyConfig
	flushInterval     time.Duration
}

// NewSlackOutputHandler はcommandごとのSlack出力先を生成する。
func NewSlackOutputHandler(queue chan *CommandOutput, reply ReplyConfig, interval time.Duration) cmd.CommandOutputHandler {
	return &slackOutputHandler{queue: queue, replyConfig: &reply, systemReplyConfig: NewSystemReplyConfig(reply.ReplyBroadcast), flushInterval: interval}
}

var _ cmd.CommandOutputHandler = (*slackOutputHandler)(nil)

func (h *slackOutputHandler) Stdout(c cmd.ConversationID, m cmd.MessageID) cmd.OutputStream {
	return newStdWriter(h.queue, h.replyConfig, c, m, h.flushInterval)
}

func (h *slackOutputHandler) Stderr(c cmd.ConversationID, m cmd.MessageID) cmd.OutputStream {
	return newErrWriter(h.queue, h.replyConfig, c, m, h.flushInterval)
}

func (h *slackOutputHandler) Start(c cmd.ConversationID, m cmd.MessageID) {
	h.queue <- &CommandOutput{ConversationID: c, MessageID: m, Spawned: true}
}

func (h *slackOutputHandler) Finish(c cmd.ConversationID, m cmd.MessageID, code int) {
	h.queue <- &CommandOutput{ConversationID: c, MessageID: m, Finished: true, ExitCode: code}
}

func (h *slackOutputHandler) SystemError(c cmd.ConversationID, m cmd.MessageID, text string, kind cmd.SystemErrorKind) {
	switch kind {
	case cmd.SystemErrorParse:
		h.queue <- &CommandOutput{ConversationID: c, MessageID: m, ReplyConfig: h.systemReplyConfig, Text: text, IsErrOut: true, ExitCode: 2}
	case cmd.SystemErrorCommandNotFound:
		stream := newErrWriter(h.queue, nil, c, m, DefaultOutputFlushInterval)
		_, _ = io.WriteString(stream, text)
		_ = stream.Flush()
	}
}

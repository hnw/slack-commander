package cmd

import (
	"io"
	"time"
)

// OutputStream はcommand出力の書き込みと最終flushを提供する。
type OutputStream interface {
	io.Writer
	Flush() error
}

// SystemErrorKind はcommandの実行前またはchain内で発生したエラーを区別する。
type SystemErrorKind int

const (
	// SystemErrorParse は受理済み入力の構文エラー。
	SystemErrorParse SystemErrorKind = iota
	// SystemErrorCommandNotFound はchain内の未解決command。
	SystemErrorCommandNotFound
)

// CommandOutputHandler は出力先と表示設定を所有する。
// Start / Finish はchain全体について先頭Commandが通知する。
type CommandOutputHandler interface {
	Stdout(ConversationID, MessageID) OutputStream
	Stderr(ConversationID, MessageID) OutputStream
	Start(ConversationID, MessageID)
	Finish(ConversationID, MessageID, int)
	SystemError(ConversationID, MessageID, string, SystemErrorKind)
}

type queuedCommandOutputHandler struct {
	queue             chan *CommandOutput
	replyConfig       interface{}
	systemReplyConfig interface{}
	flushInterval     time.Duration
}

// ConfigureOutput はstartup時にreply commandを含む出力先を設定する。
func (s *CommandSet) ConfigureOutput(queue chan *CommandOutput) {
	if s == nil {
		return
	}
	for _, command := range s.commands {
		if command == nil {
			continue
		}
		command.output = &queuedCommandOutputHandler{
			queue:             queue,
			replyConfig:       command.config.ReplyConfig,
			systemReplyConfig: command.config.SystemReplyConfig,
			flushInterval:     command.config.OutputFlushInterval,
		}
		command.replies.ConfigureOutput(queue)
	}
}

func (h *queuedCommandOutputHandler) Stdout(c ConversationID, m MessageID) OutputStream {
	return newStdWriter(h.queue, h.replyConfig, c, m, h.flushInterval)
}

func (h *queuedCommandOutputHandler) Stderr(c ConversationID, m MessageID) OutputStream {
	return newErrWriter(h.queue, h.replyConfig, c, m, h.flushInterval)
}

func (h *queuedCommandOutputHandler) Start(c ConversationID, m MessageID) {
	h.queue <- &CommandOutput{ConversationID: c, MessageID: m, Spawned: true}
}

func (h *queuedCommandOutputHandler) Finish(c ConversationID, m MessageID, code int) {
	h.queue <- &CommandOutput{ConversationID: c, MessageID: m, Finished: true, ExitCode: code}
}

func (h *queuedCommandOutputHandler) SystemError(c ConversationID, m MessageID, text string, kind SystemErrorKind) {
	if kind == SystemErrorCommandNotFound {
		stream := newErrWriter(h.queue, nil, c, m, DefaultOutputFlushInterval)
		_, _ = io.WriteString(stream, text)
		_ = stream.Flush()
		return
	}
	h.queue <- &CommandOutput{ConversationID: c, MessageID: m, ReplyConfig: h.systemReplyConfig, Text: text, IsErrOut: true, ExitCode: 2}
}

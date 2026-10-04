package cmd

import (
	"io"
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

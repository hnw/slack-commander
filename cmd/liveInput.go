package cmd

import (
	"errors"
	"io"
	"time"
)

var (
	// ErrLiveInputBusy により、listener は入力を溜めずに drop を記録できる。
	ErrLiveInputBusy = errors.New("live input buffer is full")
	// ErrLiveInputClosed は、終了と競合した入力を continuation に回さないために使う。
	ErrLiveInputClosed = errors.New("live input is closed")
)

// LiveInput は listener に interactive session の追加入力だけを公開する。
type LiveInput struct {
	*stdinSession
}

func newInteractiveStdinSession(
	initial string,
	idle time.Duration,
	onError func(error),
) *LiveInput {
	s := newStdinSession(initial, onError)
	s.replies = make(chan string, 1)
	s.idle = idle
	return &LiveInput{stdinSession: s}
}

// NewLiveInput は既存の呼び出し元で idle EOF 無効の session を接続する。
func NewLiveInput(writer io.WriteCloser, initial string, onError func(error)) *LiveInput {
	e := newInteractiveStdinSession(initial, 0, onError)
	e.Start(writer)
	return e
}

// TrySend は次の1件だけを保持し、stdin 未消費による待機の増加を防ぐ。
func (e *LiveInput) TrySend(text string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return ErrLiveInputClosed
	}
	select {
	case e.replies <- text:
		e.resetIdle()
		return nil
	default:
		return ErrLiveInputBusy
	}
}

package cmd

import (
	"errors"
	"io"
	"time"
)

var (
	// ErrInteractiveStdinBusy により、listener は入力を溜めずに drop を記録できる。
	ErrInteractiveStdinBusy = errors.New("interactive stdin buffer is full")
	// ErrInteractiveStdinClosed は、終了と競合した入力を continuation に回さないために使う。
	ErrInteractiveStdinClosed = errors.New("interactive stdin is closed")
)

// InteractiveStdin は listener に interactive session の追加入力だけを公開する。
type InteractiveStdin struct {
	*stdinSession
}

func newInteractiveStdinSession(
	initial string,
	idle time.Duration,
	onError func(error),
) *InteractiveStdin {
	s := newStdinSession(initial, onError)
	s.replies = make(chan string, 1)
	s.idle = idle
	return &InteractiveStdin{stdinSession: s}
}

// NewInteractiveStdin は既存の呼び出し元で idle EOF 無効の session を接続する。
func NewInteractiveStdin(
	writer io.WriteCloser,
	initial string,
	onError func(error),
) *InteractiveStdin {
	e := newInteractiveStdinSession(initial, 0, onError)
	e.Start(writer)
	return e
}

// TrySend は次の1件だけを保持し、stdin 未消費による待機の増加を防ぐ。
func (e *InteractiveStdin) TrySend(text string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return ErrInteractiveStdinClosed
	}
	select {
	case e.replies <- text:
		e.resetIdle()
		return nil
	default:
		return ErrInteractiveStdinBusy
	}
}

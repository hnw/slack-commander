package cmd

import (
	"errors"
	"io"
	"strings"
	"sync"
)

var (
	// ErrLiveInputBusy により、listener は入力を溜めずに drop を記録できる。
	ErrLiveInputBusy = errors.New("live input buffer is full")
	// ErrLiveInputClosed は、終了と競合した入力を continuation に回さないために使う。
	ErrLiveInputClosed = errors.New("live input is closed")
)

// LiveInput は listener を blocking stdin write から切り離す。
type LiveInput struct {
	mu       sync.Mutex
	closed   bool
	writer   io.WriteCloser
	replies  chan string
	stop     chan struct{}
	finished chan struct{}
}

// NewLiveInput は公開直後の reply が初期入力を追い越さないように転送順を固定する。
func NewLiveInput(writer io.WriteCloser, initial string, onError func(error)) *LiveInput {
	e := &LiveInput{
		writer:   writer,
		replies:  make(chan string, 1),
		stop:     make(chan struct{}),
		finished: make(chan struct{}),
	}
	go e.forward(initial, onError)
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
		return nil
	default:
		return ErrLiveInputBusy
	}
}

// Close は blocked write を解除し、終了後に転送処理が残るのを防ぐ。
func (e *LiveInput) Close() {
	e.closeInput()
	<-e.finished
}

func (e *LiveInput) closeInput() {
	e.mu.Lock()
	if e.closed {
		e.mu.Unlock()
		return
	}
	e.closed = true
	close(e.stop)
	e.mu.Unlock()
	_ = e.writer.Close()
}

func (e *LiveInput) forward(initial string, onError func(error)) {
	defer close(e.finished)
	defer e.closeInput()
	write := func(text string) bool {
		select {
		case <-e.stop:
			return false
		default:
		}
		if !strings.HasSuffix(text, "\n") {
			text += "\n"
		}
		if _, err := io.WriteString(e.writer, text); err != nil {
			e.mu.Lock()
			closed := e.closed
			e.mu.Unlock()
			if !closed && onError != nil {
				onError(err)
			}
			return false
		}
		return true
	}
	if initial != "" && !write(initial) {
		return
	}
	for {
		select {
		case <-e.stop:
			return
		case text := <-e.replies:
			if !write(text) {
				return
			}
		}
	}
}

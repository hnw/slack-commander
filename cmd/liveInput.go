package cmd

import (
	"errors"
	"io"
	"strings"
	"sync"
)

var (
	ErrLiveInputBusy   = errors.New("live input buffer is full")
	ErrLiveInputClosed = errors.New("live input is closed")
)

type LiveInput struct {
	mu       sync.Mutex
	closed   bool
	writer   io.WriteCloser
	replies  chan string
	stop     chan struct{}
	finished chan struct{}
}

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

package cmd

import (
	"io"
	"strings"
	"sync"
	"time"
)

type stdinSession struct {
	mu         sync.Mutex
	closed     bool
	writer     io.WriteCloser
	initial    string
	lineEnding string
	onError    func(error)
	onClose    func()
	replies    chan string
	stop       chan struct{}
	finished   chan struct{}
	idle       time.Duration
	deadline   time.Time
	timer      *time.Timer
}

func newStdinSession(initial string, onError func(error)) *stdinSession {
	return &stdinSession{
		initial: initial, lineEnding: "\n", onError: onError,
		stop: make(chan struct{}), finished: make(chan struct{}),
	}
}

func (s *stdinSession) Start(writer io.WriteCloser) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		_ = writer.Close()
		return
	}
	s.writer = writer
	s.resetIdle()
	go s.forward()
}

func (s *stdinSession) resetIdle() {
	if s.idle <= 0 {
		return
	}
	s.deadline = time.Now().Add(s.idle)
	if s.timer == nil {
		s.timer = time.AfterFunc(s.idle, s.expire)
	} else {
		s.timer.Reset(s.idle)
	}
}

func (s *stdinSession) expire() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	// Reset 前に発火済みの callback も、最後に受理した入力の期限で判定する。
	if remaining := time.Until(s.deadline); remaining > 0 {
		s.timer.Reset(remaining)
		return
	}
	s.closeLocked()
}

func (s *stdinSession) Close() {
	s.closeInput()
	<-s.finished
}

func (s *stdinSession) closeInput() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closeLocked()
}

func (s *stdinSession) closeLocked() {
	if s.closed {
		return
	}
	s.closed = true
	if s.timer != nil {
		s.timer.Stop()
	}
	close(s.stop)
	if s.writer != nil {
		_ = s.writer.Close()
	} else {
		close(s.finished)
	}
	if s.onClose != nil {
		s.onClose()
	}
}

func (s *stdinSession) write(text string) bool {
	select {
	case <-s.stop:
		return false
	default:
	}
	if s.replies != nil {
		text = ensureInputTerminator(text, s.lineEnding)
	}
	if _, err := io.WriteString(s.writer, text); err != nil {
		s.mu.Lock()
		closed := s.closed
		s.mu.Unlock()
		if !closed && s.onError != nil {
			s.onError(err)
		}
		return false
	}
	return true
}

func ensureInputTerminator(text, lineEnding string) string {
	if lineEnding == "\r" {
		switch {
		case strings.HasSuffix(text, "\r\n"):
			text = text[:len(text)-2]
		case strings.HasSuffix(text, "\n"):
			text = text[:len(text)-1]
		case strings.HasSuffix(text, "\r"):
			return text
		}
		return text + "\r"
	}
	if strings.HasSuffix(text, "\n") {
		return text
	}
	return text + "\n"
}

func (s *stdinSession) forward() {
	defer close(s.finished)
	defer s.closeInput()
	if s.initial != "" && !s.write(s.initial) {
		return
	}
	if s.replies == nil {
		return
	}
	for {
		select {
		case <-s.stop:
			return
		case text := <-s.replies:
			if !s.write(text) {
				return
			}
		}
	}
}

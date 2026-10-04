package pubsub

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/hnw/slack-commander/cmd"
)

// DefaultOutputFlushInterval is the maximum delay before buffered output is emitted.
const DefaultOutputFlushInterval = time.Second

type slackOutputStream struct {
	bufw          *bufio.Writer // 埋め込みにするとWriteメソッドの上書きができない場合があったのでメンバにしている
	raw           *rawWriter
	flushInterval time.Duration
	timer         *time.Timer
	timerSequence uint64
	mu            sync.Mutex
}

func newStdWriter(
	ch chan<- *slackOutputEvent,
	cfg *ReplyConfig,
	conversationID cmd.ConversationID,
	messageID cmd.MessageID,
	flushInterval time.Duration,
) *slackOutputStream {
	return newSlackOutputStream(ch, cfg, false, conversationID, messageID, flushInterval)
}

func newErrWriter(
	ch chan<- *slackOutputEvent,
	cfg *ReplyConfig,
	conversationID cmd.ConversationID,
	messageID cmd.MessageID,
	flushInterval time.Duration,
) *slackOutputStream {
	return newSlackOutputStream(ch, cfg, true, conversationID, messageID, flushInterval)
}

func newSlackOutputStream(
	ch chan<- *slackOutputEvent,
	cfg *ReplyConfig,
	isErrOut bool,
	conversationID cmd.ConversationID,
	messageID cmd.MessageID,
	flushInterval time.Duration,
) *slackOutputStream {
	raw := newRawWriter(ch, cfg, isErrOut, conversationID, messageID)
	return &slackOutputStream{
		bufw:          bufio.NewWriterSize(raw, 2048),
		raw:           raw,
		flushInterval: flushInterval,
	}
}

func (w *slackOutputStream) Write(data []byte) (n int, err error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	n, err = w.bufw.Write(data)
	if err != nil {
		return n, err
	}
	if w.bufw.Buffered() == 0 {
		w.stopTimerLocked()
		return n, nil
	}
	if w.flushInterval == 0 {
		return n, w.bufw.Flush()
	}
	if w.timer == nil {
		w.startTimerLocked()
	}
	return
}

func (w *slackOutputStream) Flush() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.stopTimerLocked()
	if err := w.bufw.Flush(); err != nil {
		return err
	}
	return w.raw.Flush()
}

func (w *slackOutputStream) startTimerLocked() {
	w.timerSequence++
	sequence := w.timerSequence
	w.timer = time.AfterFunc(w.flushInterval, func() {
		w.flushBuffered(sequence)
	})
}

func (w *slackOutputStream) stopTimerLocked() {
	if w.timer != nil {
		w.timer.Stop()
		w.timer = nil
	}
	w.timerSequence++
}

func (w *slackOutputStream) flushBuffered(sequence uint64) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.timerSequence != sequence {
		return
	}
	w.timer = nil
	_ = w.bufw.Flush()
}

type rawWriter struct {
	Ch             chan<- *slackOutputEvent
	ReplyConfig    *ReplyConfig
	ConversationID cmd.ConversationID
	MessageID      cmd.MessageID
	IsErrOut       bool
	buf            []byte
}

func newRawWriter(
	ch chan<- *slackOutputEvent,
	cfg *ReplyConfig,
	isErrOut bool,
	conversationID cmd.ConversationID,
	messageID cmd.MessageID,
) *rawWriter {
	return &rawWriter{
		Ch:             ch,
		ReplyConfig:    cfg,
		ConversationID: conversationID,
		MessageID:      messageID,
		IsErrOut:       isErrOut,
	}
}

func (w *rawWriter) emitText(text []byte) {
	if len(text) == 0 {
		return
	}
	w.Ch <- &slackOutputEvent{
		ReplyConfig:    w.ReplyConfig,
		ConversationID: w.ConversationID,
		MessageID:      w.MessageID,
		Text:           string(text),
		IsErrOut:       w.IsErrOut,
	}
}

func (w *rawWriter) emitImage(sixelData []byte) {
	pngBytes, err := sixelToPNG(sixelData)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[WARN] sixel to PNG conversion failed: %v\n", err)
		return
	}
	w.Ch <- &slackOutputEvent{
		ReplyConfig:    w.ReplyConfig,
		ConversationID: w.ConversationID,
		MessageID:      w.MessageID,
		ImageData:      pngBytes,
		IsErrOut:       w.IsErrOut,
	}
}

func (w *rawWriter) Write(data []byte) (n int, err error) {
	w.buf = append(w.buf, data...)
	w.processBuffer(false)
	return len(data), nil
}

func (w *rawWriter) processBuffer(final bool) {
	dcsStart := []byte{0x1b, 'P'}
	dcsEnd := []byte{0x1b, '\\'}

	for len(w.buf) > 0 {
		start := bytes.Index(w.buf, dcsStart)
		if start == -1 {
			if final {
				w.emitText(w.buf)
				w.buf = w.buf[:0]
				return
			}
			if w.buf[len(w.buf)-1] == 0x1b {
				w.emitText(w.buf[:len(w.buf)-1])
				w.buf = w.buf[len(w.buf)-1:]
				return
			}
			w.emitText(w.buf)
			w.buf = w.buf[:0]
			return
		}

		if start > 0 {
			w.emitText(w.buf[:start])
			w.buf = w.buf[start:]
			continue
		}

		end := bytes.Index(w.buf, dcsEnd)
		if end == -1 {
			if final {
				w.buf = w.buf[:0]
			}
			return
		}

		dcsData := w.buf[:end+len(dcsEnd)]
		w.buf = w.buf[end+len(dcsEnd):]
		w.emitImage(dcsData)
	}
}

// Incomplete sixel sequences must not carry over into subsequent output.
func (w *rawWriter) Flush() error {
	w.processBuffer(true)
	return nil
}

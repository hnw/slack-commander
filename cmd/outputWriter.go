package cmd

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"sync"
	"time"
)

type OutputWriter struct {
	bufw  *bufio.Writer // 埋め込みにするとWriteメソッドの上書きができない場合があったのでメンバにしている
	raw   *rawWriter
	timer *time.Timer
	mu    sync.Mutex
}

func newStdWriter(ch chan *CommandOutput, replyInfo interface{}, cfg interface{}) *OutputWriter {
	return newOutputWriter(ch, replyInfo, cfg, false)
}

func newErrWriter(ch chan *CommandOutput, replyInfo interface{}, cfg interface{}) *OutputWriter {
	return newOutputWriter(ch, replyInfo, cfg, true)
}

func newOutputWriter(
	ch chan *CommandOutput,
	replyInfo interface{},
	cfg interface{},
	isErrOut bool,
) *OutputWriter {
	raw := newRawWriter(ch, replyInfo, cfg, isErrOut)
	return &OutputWriter{
		bufw: bufio.NewWriterSize(raw, 2048),
		raw:  raw,
	}
}

func (w *OutputWriter) Write(data []byte) (n int, err error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.timer != nil {
		w.timer.Stop()
	}
	n, err = w.bufw.Write(data)
	if w.timer == nil {
		w.timer = time.AfterFunc(3*time.Second, w.flushLocked)
	} else {
		w.timer.Reset(3 * time.Second)
	}
	return
}

func (w *OutputWriter) Flush() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.timer != nil {
		w.timer.Stop()
	}
	if err := w.bufw.Flush(); err != nil {
		return err
	}
	return w.raw.Flush()
}

func (w *OutputWriter) flushLocked() {
	w.mu.Lock()
	defer w.mu.Unlock()
	_ = w.bufw.Flush()
	_ = w.raw.Flush()
}

type rawWriter struct {
	Ch          chan *CommandOutput
	ReplyInfo   interface{}
	ReplyConfig interface{}
	IsErrOut    bool
	buf         []byte
}

func newRawWriter(
	ch chan *CommandOutput,
	replyInfo interface{},
	cfg interface{},
	isErrOut bool,
) *rawWriter {
	return &rawWriter{
		Ch:          ch,
		ReplyInfo:   replyInfo,
		ReplyConfig: cfg,
		IsErrOut:    isErrOut,
	}
}

func (w *rawWriter) emitText(text []byte) {
	if len(text) == 0 {
		return
	}
	w.Ch <- &CommandOutput{
		ReplyInfo:   w.ReplyInfo,
		ReplyConfig: w.ReplyConfig,
		Text:        string(text),
		IsErrOut:    w.IsErrOut,
	}
}

func (w *rawWriter) emitImage(sixelData []byte) {
	pngBytes, err := sixelToPNG(sixelData)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[WARN] sixel to PNG conversion failed: %v\n", err)
		return
	}
	w.Ch <- &CommandOutput{
		ReplyInfo:   w.ReplyInfo,
		ReplyConfig: w.ReplyConfig,
		ImageData:   pngBytes,
		IsErrOut:    w.IsErrOut,
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

// Flush は rawWriter に残ったバッファを処理する。
// 不完全な sixel シーケンスは破棄し、残テキストは送信する。
func (w *rawWriter) Flush() error {
	w.processBuffer(true)
	return nil
}

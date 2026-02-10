package cmd

import (
	"bufio"
	"sync"
	"time"
)

type OutputWriter struct {
	bufw  *bufio.Writer // 埋め込みにするとWriteメソッドの上書きができない場合があったのでメンバにしている
	timer *time.Timer
	mu    sync.Mutex
}

func newStdWriter(ch chan *CommandOutput, replyInfo interface{}, cfg interface{}) *OutputWriter {
	return newOutputWriter(ch, replyInfo, cfg, false)
}

func newErrWriter(ch chan *CommandOutput, replyInfo interface{}, cfg interface{}) *OutputWriter {
	return newOutputWriter(ch, replyInfo, cfg, true)
}

func newOutputWriter(ch chan *CommandOutput, replyInfo interface{}, cfg interface{}, isErrOut bool) *OutputWriter {
	return &OutputWriter{
		bufw: bufio.NewWriterSize(newRawWriter(ch, replyInfo, cfg, isErrOut), 2048),
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
	return w.bufw.Flush()
}

func (w *OutputWriter) flushLocked() {
	w.mu.Lock()
	defer w.mu.Unlock()
	_ = w.bufw.Flush()
}

type rawWriter struct {
	Ch          chan *CommandOutput
	ReplyInfo   interface{}
	ReplyConfig interface{}
	IsErrOut    bool
}

func newRawWriter(ch chan *CommandOutput, replyInfo interface{}, cfg interface{}, isErrOut bool) *rawWriter {
	return &rawWriter{
		Ch:          ch,
		ReplyInfo:   replyInfo,
		ReplyConfig: cfg,
		IsErrOut:    isErrOut,
	}
}

func (w *rawWriter) Write(data []byte) (n int, err error) {
	w.Ch <- &CommandOutput{
		ReplyInfo:   w.ReplyInfo,
		ReplyConfig: w.ReplyConfig,
		Text:        string(data),
		IsErrOut:    w.IsErrOut,
	}
	return len(data), nil
}

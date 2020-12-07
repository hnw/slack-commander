package cmd

import (
	"bufio"
	"time"
)

type OutputWriter struct {
	bufw  *bufio.Writer
	timer *time.Timer
}

func newStdWriter(ch chan *CommandOutput, replyInfo interface{}, cfg interface{}) *OutputWriter {
	return newOutputWriter(ch, replyInfo, cfg, false)
}

func newErrWriter(ch chan *CommandOutput, replyInfo interface{}, cfg interface{}) *OutputWriter {
	return newOutputWriter(ch, replyInfo, cfg, true)
}

func newOutputWriter(ch chan *CommandOutput, replyInfo interface{}, cfg interface{}, isError bool) *OutputWriter {
	return &OutputWriter{
		bufw: bufio.NewWriterSize(newRawWriter(ch, replyInfo, cfg, isError), 2048),
	}
}

func (w *OutputWriter) Write(data []byte) (n int, err error) {
	if w.timer != nil {
		w.timer.Stop()
	}
	n, err = w.bufw.Write(data)
	w.timer = time.AfterFunc(3*time.Second, func() {
		w.timer.Stop()
		w.bufw.Flush()
	})
	return
}

func (w *OutputWriter) Flush() error {
	return w.bufw.Flush()
}

type rawWriter struct {
	Ch          chan *CommandOutput
	ReplyInfo   interface{}
	ReplyConfig interface{}
	IsError     bool
}

func newRawWriter(ch chan *CommandOutput, replyInfo interface{}, cfg interface{}, isError bool) *rawWriter {
	return &rawWriter{
		Ch:          ch,
		ReplyInfo:   replyInfo,
		ReplyConfig: cfg,
		IsError:     isError,
	}
}

func (w *rawWriter) Write(data []byte) (n int, err error) {
	w.Ch <- &CommandOutput{
		ReplyInfo:   w.ReplyInfo,
		ReplyConfig: w.ReplyConfig,
		Text:        string(data),
	}
	return len(data), nil
}

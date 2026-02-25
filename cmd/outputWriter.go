package cmd

import (
	"bufio"
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
	// sixel ステートマシン（Write() 呼び出しをまたいで状態を保持）
	state    sixelState
	textBuf  []byte
	sixelBuf []byte
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

func (w *rawWriter) emitText() {
	if len(w.textBuf) > 0 {
		w.Ch <- &CommandOutput{
			ReplyInfo:   w.ReplyInfo,
			ReplyConfig: w.ReplyConfig,
			Text:        string(w.textBuf),
			IsErrOut:    w.IsErrOut,
		}
		w.textBuf = w.textBuf[:0]
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
	for _, b := range data {
		w.processOneByte(b)
	}
	if w.state == sixelStateText {
		w.emitText()
	}
	return len(data), nil
}

// processOneByte は 1 バイトを受け取り、現在のステートに応じて遷移させる
func (w *rawWriter) processOneByte(b byte) {
	switch w.state {
	case sixelStateText:
		w.processTextByte(b)
	case sixelStateESC:
		w.processESCByte(b)
	case sixelStateDCS:
		w.processDCSByte(b)
	case sixelStateDCSESC:
		w.processDCSESCByte(b)
	case sixelStateData:
		w.processDataByte(b)
	case sixelStateDataESC:
		w.processDataESCByte(b)
	}
}

func (w *rawWriter) processTextByte(b byte) {
	if b == 0x1b {
		// \x1b を見たら先行テキストを送信してからエスケープ処理へ
		w.emitText()
		w.state = sixelStateESC
	} else {
		w.textBuf = append(w.textBuf, b)
	}
}

func (w *rawWriter) processESCByte(b byte) {
	switch b {
	case 'P':
		// DCS 開始確定
		w.sixelBuf = append(w.sixelBuf[:0], 0x1b, 'P')
		w.state = sixelStateDCS
	case 0x1b:
		// 直前の \x1b はエスケープ文字としてテキストへ戻す。新しい \x1b を保留
		w.textBuf = append(w.textBuf, 0x1b)
		w.emitText()
		// state は sixelStateESC のまま（新しい \x1b を待機）
	default:
		// DCS ではなかった。\x1b と b をテキストとして扱う
		w.textBuf = append(w.textBuf, 0x1b, b)
		w.state = sixelStateText
	}
}

func (w *rawWriter) processDCSByte(b byte) {
	w.sixelBuf = append(w.sixelBuf, b)
	switch b {
	case 'q':
		// sixel 確定
		w.state = sixelStateData
	case 0x1b:
		w.state = sixelStateDCSESC
	}
}

func (w *rawWriter) processDCSESCByte(b byte) {
	switch b {
	case '\\':
		// ST を受信: non-sixel DCS として破棄
		w.sixelBuf = w.sixelBuf[:0]
		w.state = sixelStateText
	case 0x1b:
		// 直前の \x1b は ST の一部ではなかった
		w.sixelBuf = append(w.sixelBuf, 0x1b)
		// state は sixelStateDCSESC のまま（新しい \x1b を待機）
	default:
		w.sixelBuf = append(w.sixelBuf, b)
		w.state = sixelStateDCS
	}
}

func (w *rawWriter) processDataByte(b byte) {
	w.sixelBuf = append(w.sixelBuf, b)
	if b == 0x1b {
		w.state = sixelStateDataESC
	}
}

func (w *rawWriter) processDataESCByte(b byte) {
	w.sixelBuf = append(w.sixelBuf, b)
	switch b {
	case '\\':
		// ST を受信: sixel 完了
		w.emitImage(w.sixelBuf)
		w.sixelBuf = w.sixelBuf[:0]
		w.state = sixelStateText
	case 0x1b:
		// 直前の \x1b は ST の一部ではなかった
		// state は sixelStateDataESC のまま（新しい \x1b を待機）
	default:
		w.state = sixelStateData
	}
}

// Flush は rawWriter に残ったバッファを処理する。
// 不完全な sixel シーケンスは破棄し、残テキストは送信する。
func (w *rawWriter) Flush() error {
	switch w.state {
	case sixelStateText:
		w.emitText()
	case sixelStateESC:
		// 保留中の \x1b をテキストとして扱う
		w.textBuf = append(w.textBuf, 0x1b)
		w.emitText()
	default:
		// DCS / sixel 途中で中断した場合は破棄
		w.sixelBuf = w.sixelBuf[:0]
	}
	w.state = sixelStateText
	return nil
}

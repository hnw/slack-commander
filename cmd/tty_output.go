package cmd

import (
	"bytes"
	"io"
	"sync"
)

type ttyOutputState uint8

const (
	ttyOutputText ttyOutputState = iota
	ttyOutputEscape
	ttyOutputCSI
	ttyOutputOSC
	ttyOutputDCS
	ttyOutputString
	ttyOutputStringEscape
)

// ttyOutputNormalizer strips terminal control sequences without emulating a terminal.
type ttyOutputNormalizer struct {
	dst     io.Writer
	mu      sync.Mutex
	state   ttyOutputState
	string  ttyOutputState
	lastCR  bool
	pending bytes.Buffer
}

func newTTYOutputNormalizer(dst io.Writer) *ttyOutputNormalizer {
	return &ttyOutputNormalizer{dst: dst}
}

func (w *ttyOutputNormalizer) Write(data []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if err := flushTTYPlain(w.dst, &w.pending); err != nil {
		return 0, err
	}
	for i, b := range data {
		if w.state == ttyOutputText && isTTYControlStart(b) {
			if err := flushTTYPlain(w.dst, &w.pending); err != nil {
				return i, err
			}
		}
		w.writeByte(&w.pending, b)
	}
	if err := flushTTYPlain(w.dst, &w.pending); err != nil {
		return len(data), err
	}
	return len(data), nil
}

func isTTYControlStart(b byte) bool {
	return b == 0x1b
}

func flushTTYPlain(dst io.Writer, plain *bytes.Buffer) error {
	for plain.Len() > 0 {
		remaining := plain.Len()
		n, err := dst.Write(plain.Bytes())
		if n < 0 || n > remaining {
			return io.ErrShortWrite
		}
		if n > 0 {
			plain.Next(n)
		}
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
	}
	return nil
}

//nolint:gocyclo // Terminal control sequences require explicit states to preserve chunk boundaries.
func (w *ttyOutputNormalizer) writeByte(plain *bytes.Buffer, b byte) {
	switch w.state {
	case ttyOutputText:
		switch b {
		case 0x1b:
			w.state = ttyOutputEscape
			return
		case '\r':
			w.lastCR = true
			plain.WriteByte('\n')
			return
		}
		if b == '\n' && w.lastCR {
			w.lastCR = false
			return
		}
		w.lastCR = false
		plain.WriteByte(b)
	case ttyOutputEscape:
		switch b {
		case '[':
			w.state = ttyOutputCSI
		case ']':
			w.state = ttyOutputOSC
		case 'P':
			w.state = ttyOutputDCS
		case '^', '_', 'X':
			w.state = ttyOutputString
		default:
			if b >= 0x20 && b <= 0x2f {
				return
			}
			w.state = ttyOutputText
		}
		return
	case ttyOutputCSI:
		if b >= 0x40 && b <= 0x7e {
			w.state = ttyOutputText
		}
		return
	case ttyOutputOSC:
		switch b {
		case 0x07:
			w.state = ttyOutputText
		case 0x1b:
			w.string = ttyOutputOSC
			w.state = ttyOutputStringEscape
		}
		return
	case ttyOutputDCS:
		switch b {
		case 0x1b:
			w.string = ttyOutputDCS
			w.state = ttyOutputStringEscape
		}
		return
	case ttyOutputString:
		switch b {
		case 0x1b:
			w.string = ttyOutputString
			w.state = ttyOutputStringEscape
		}
		return
	case ttyOutputStringEscape:
		if b == '\\' {
			w.state = ttyOutputText
		} else if b != 0x1b {
			w.state = w.string
		}
		return
	default:
		return
	}
}

func (w *ttyOutputNormalizer) Flush() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if err := flushTTYPlain(w.dst, &w.pending); err != nil {
		return err
	}
	w.state = ttyOutputText
	w.lastCR = false
	if flusher, ok := w.dst.(interface{ Flush() error }); ok {
		return flusher.Flush()
	}
	return nil
}

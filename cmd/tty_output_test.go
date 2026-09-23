package cmd

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
)

type recordingWriter struct {
	writes [][]byte
}

func (w *recordingWriter) Write(data []byte) (int, error) {
	w.writes = append(w.writes, append([]byte(nil), data...))
	return len(data), nil
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) {
	return 0, errors.New("write failed")
}

type partialWriter struct {
	max    int
	writes [][]byte
}

func (w *partialWriter) Write(data []byte) (int, error) {
	n := min(w.max, len(data))
	w.writes = append(w.writes, append([]byte(nil), data[:n]...))
	return n, nil
}

type writeResult struct {
	n   int
	err error
}

type scriptedWriter struct {
	results []writeResult
	output  []byte
}

func (w *scriptedWriter) Write(data []byte) (int, error) {
	result := writeResult{n: len(data)}
	if len(w.results) > 0 {
		result, w.results = w.results[0], w.results[1:]
	}
	w.output = append(w.output, data[:result.n]...)
	return result.n, result.err
}

type zeroWriter struct{}

func (zeroWriter) Write([]byte) (int, error) {
	return 0, nil
}

func TestTTYOutputNormalizer(t *testing.T) {
	tests := []struct {
		name   string
		writes []string
		want   string
	}{
		{name: "plain text", writes: []string{"hello"}, want: "hello"},
		{name: "CSI color", writes: []string{"\x1b[31mred\x1b[0m"}, want: "red"},
		{name: "OSC title", writes: []string{"\x1b]0;title\x07text"}, want: "text"},
		{
			name: "DCS and sixel", writes: []string{"before\x1bPqSIXEL\x1b\\after"}, want: "beforeafter",
		},
		{name: "APC", writes: []string{"before\x1b_payload\x1b\\after"}, want: "beforeafter"},
		{
			name: "raw C1 bytes are text", writes: []string{"before\x9bafter"}, want: "before\x9bafter",
		},
		{
			name: "ESC character set", writes: []string{"before\x1b(Bafter"}, want: "beforeafter",
		},
		{
			name: "ESC character set alternate", writes: []string{"before\x1b)0after"}, want: "beforeafter",
		},
		{
			name: "ESC percent", writes: []string{"before\x1b%Gafter"}, want: "beforeafter",
		},
		{
			name: "ESC hash", writes: []string{"before\x1b#8after"}, want: "beforeafter",
		},
		{
			name: "split ESC sequence", writes: []string{"before\x1b(", "Bafter"}, want: "beforeafter",
		},
		{
			name:   "split sequences",
			writes: []string{"a\x1b[3", "1mb\x1b]0;ti", "tle\x1b\\c\x1bPq", "data\x1b\\d"},
			want:   "abcd",
		},
		{
			name: "normalizes carriage returns", writes: []string{"one\r\ntwo\rthree"},
			want: "one\ntwo\nthree",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out bytes.Buffer
			normalizer := newTTYOutputNormalizer(&out)
			for _, write := range tt.writes {
				if _, err := normalizer.Write([]byte(write)); err != nil {
					t.Fatal(err)
				}
			}
			if err := normalizer.Flush(); err != nil {
				t.Fatal(err)
			}
			if got := out.String(); got != tt.want {
				t.Fatalf("output = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestTTYOutputNormalizerPreservesUTF8(t *testing.T) {
	input := "日本語のタスク一覧です。住所変更せ"
	if !bytes.Contains([]byte(input), []byte{0x9b}) {
		t.Fatal("test input does not contain UTF-8 byte 0x9b")
	}
	var out bytes.Buffer
	normalizer := newTTYOutputNormalizer(&out)
	if _, err := normalizer.Write([]byte(input)); err != nil {
		t.Fatal(err)
	}
	if err := normalizer.Flush(); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); got != input {
		t.Fatalf("output=%q, want=%q", got, input)
	}
}

func TestTTYOutputNormalizerBatchesPlainText(t *testing.T) {
	var dst recordingWriter
	normalizer := newTTYOutputNormalizer(&dst)
	input := strings.Repeat("x", 4096)
	if n, err := normalizer.Write([]byte(input)); err != nil || n != len(input) {
		t.Fatalf("Write() = (%d, %v)", n, err)
	}
	if len(dst.writes) != 1 || string(dst.writes[0]) != input {
		t.Fatalf("writes=%q", dst.writes)
	}
}

func TestTTYOutputNormalizerBatchesTextAroundEscapeSequences(t *testing.T) {
	var dst recordingWriter
	normalizer := newTTYOutputNormalizer(&dst)
	if _, err := normalizer.Write([]byte("left\x1b[31mright")); err != nil {
		t.Fatal(err)
	}
	if got, want := len(dst.writes), 2; got != want ||
		string(dst.writes[0]) != "left" || string(dst.writes[1]) != "right" {
		t.Fatalf("writes=%q", dst.writes)
	}
}

func TestTTYOutputNormalizerRetriesPartialWrites(t *testing.T) {
	dst := &partialWriter{max: 3}
	normalizer := newTTYOutputNormalizer(dst)
	input := "batched output"
	if n, err := normalizer.Write([]byte(input)); err != nil || n != len(input) {
		t.Fatalf("Write() = (%d, %v)", n, err)
	}
	var output []byte
	for _, write := range dst.writes {
		output = append(output, write...)
	}
	if string(output) != input {
		t.Fatalf("output=%q, want %q", output, input)
	}
	if len(dst.writes) < 2 {
		t.Fatalf("writes=%q, want multiple writes", dst.writes)
	}
}

func TestTTYOutputNormalizerRetainsPendingAfterPartialWriteError(t *testing.T) {
	dst := &scriptedWriter{results: []writeResult{{n: 2, err: errors.New("write failed")}}}
	normalizer := newTTYOutputNormalizer(dst)
	if _, err := normalizer.Write([]byte("plain")); err == nil {
		t.Fatal("Write() succeeded, want error")
	}
	if got, want := string(dst.output), "pl"; got != want {
		t.Fatalf("output=%q, want %q", got, want)
	}
	if _, err := normalizer.Write([]byte("new")); err != nil {
		t.Fatal(err)
	}
	if got, want := string(dst.output), "plainnew"; got != want {
		t.Fatalf("output=%q, want %q", got, want)
	}
}

func TestTTYOutputNormalizerRetainsPendingAfterZeroByteWriteError(t *testing.T) {
	dst := &scriptedWriter{results: []writeResult{{n: 0, err: errors.New("write failed")}}}
	normalizer := newTTYOutputNormalizer(dst)
	if _, err := normalizer.Write([]byte("plain")); err == nil {
		t.Fatal("Write() succeeded, want error")
	}
	if _, err := normalizer.Write([]byte("new")); err != nil {
		t.Fatal(err)
	}
	if got, want := string(dst.output), "plainnew"; got != want {
		t.Fatalf("output=%q, want %q", got, want)
	}
}

func TestTTYOutputNormalizerDoesNotConsumeNewInputWhenPendingFlushFails(t *testing.T) {
	errWrite := errors.New("write failed")
	dst := &scriptedWriter{results: []writeResult{{n: 0, err: errWrite}, {n: 0, err: errWrite}}}
	normalizer := newTTYOutputNormalizer(dst)
	if _, err := normalizer.Write([]byte("plain")); !errors.Is(err, errWrite) {
		t.Fatalf("Write() error = %v, want %v", err, errWrite)
	}
	if n, err := normalizer.Write([]byte("new")); n != 0 || !errors.Is(err, errWrite) {
		t.Fatalf("Write() = (%d, %v), want (0, %v)", n, err, errWrite)
	}
	if _, err := normalizer.Write([]byte("after")); err != nil {
		t.Fatal(err)
	}
	if got, want := string(dst.output), "plainafter"; got != want {
		t.Fatalf("output=%q, want %q", got, want)
	}
}

func TestTTYOutputNormalizerFlushRetainsPendingAfterError(t *testing.T) {
	errWrite := errors.New("write failed")
	dst := &scriptedWriter{results: []writeResult{{n: 0, err: errWrite}, {n: 0, err: errWrite}}}
	normalizer := newTTYOutputNormalizer(dst)
	if _, err := normalizer.Write([]byte("plain")); !errors.Is(err, errWrite) {
		t.Fatalf("Write() error = %v, want %v", err, errWrite)
	}
	if err := normalizer.Flush(); !errors.Is(err, errWrite) {
		t.Fatalf("Flush() error = %v, want %v", err, errWrite)
	}
	if err := normalizer.Flush(); err != nil {
		t.Fatal(err)
	}
	if got, want := string(dst.output), "plain"; got != want {
		t.Fatalf("output=%q, want %q", got, want)
	}
}

func TestTTYOutputNormalizerRejectsZeroByteWrite(t *testing.T) {
	normalizer := newTTYOutputNormalizer(zeroWriter{})
	if _, err := normalizer.Write([]byte("plain")); !errors.Is(err, io.ErrShortWrite) {
		t.Fatalf("Write() error = %v, want io.ErrShortWrite", err)
	}
}

func TestTTYOutputNormalizerReportsConsumedInputBeforeWriteError(t *testing.T) {
	normalizer := newTTYOutputNormalizer(failingWriter{})
	input := []byte("plain\x1b[31mred")
	n, err := normalizer.Write(input)
	if err == nil || n != len("plain") {
		t.Fatalf("Write() = (%d, %v), want (%d, error)", n, err, len("plain"))
	}
}

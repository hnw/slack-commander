package messaging

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/hnw/slack-commander/internal/command"
)

func TestSlackOutputStreamUTF8Boundaries(t *testing.T) {
	for _, tc := range []struct {
		name     string
		text     string
		writes   [][]byte
		interval time.Duration
		timer    bool
	}{
		{"buffer", strings.Repeat("a", 2047) + "日本語", [][]byte{[]byte(strings.Repeat("a", 2047)), []byte("日本語")}, time.Hour, false},
		{"writes", "é日😀", [][]byte{{0xc3}, {0xa9, 0xe6}, {0x97}, {0xa5, 0xf0}, {0x9f}, {0x98}, {0x80}}, 0, false},
		{"timer", "日本語", [][]byte{{0xe6}, {0x97}, {0xa5}, []byte("本語")}, 20 * time.Millisecond, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			writer, ch := newTestSlackOutputStream(tc.interval)
			var text strings.Builder
			for _, data := range tc.writes {
				if _, err := writer.Write(data); err != nil {
					t.Fatal(err)
				}
				if tc.timer {
					waitForTimerCallback(t, writer)
				}
				for len(ch) > 0 {
					output := <-ch
					if !utf8.ValidString(output.Text) {
						t.Fatalf("invalid UTF-8 before final flush: %q", output.Text)
					}
					text.WriteString(output.Text)
				}
			}
			if err := writer.Flush(); err != nil {
				t.Fatal(err)
			}
			close(ch)
			for output := range ch {
				if !utf8.ValidString(output.Text) {
					t.Fatalf("invalid UTF-8: %q", output.Text)
				}
				text.WriteString(output.Text)
			}
			if text.String() != tc.text {
				t.Fatalf("output = %q, want %q", text.String(), tc.text)
			}
		})
	}
}

func TestRawWriterUTF8SplitPoints(t *testing.T) {
	for _, text := range []string{"é", "日", "😀"} {
		for split := 1; split < len(text); split++ {
			t.Run(fmt.Sprintf("%s/%d", text, split), func(t *testing.T) {
				outs := collectRawOutputs(t, []byte("前"+text[:split]), []byte(text[split:]+"後"))
				var combined strings.Builder
				for _, output := range outs {
					if !utf8.ValidString(output.Text) {
						t.Fatalf("invalid UTF-8: %q", output.Text)
					}
					combined.WriteString(output.Text)
				}
				if combined.String() != "前"+text+"後" {
					t.Fatalf("output = %q, want %q", combined.String(), "前"+text+"後")
				}
			})
		}
	}
}

func TestRawWriterUTF8WithSixel(t *testing.T) {
	outs := collectRawOutputs(t, []byte{0xe6}, append([]byte{0x97, 0xa5}, minimalRedSixel[:1]...), minimalRedSixel[1:], []byte{0xf0, 0x9f}, []byte{0x98, 0x80})
	if len(outs) != 3 {
		t.Fatalf("output count = %d, want 3", len(outs))
	}
	if outs[0].Text != "日" || !bytes.HasPrefix(outs[1].ImageData, []byte("\x89PNG")) || outs[2].Text != "😀" {
		t.Fatalf("expected text, PNG, text; got %+v", outs)
	}
	for _, output := range outs {
		if !utf8.ValidString(output.Text) {
			t.Fatalf("invalid UTF-8: %q", output.Text)
		}
	}
	if outs[0].Text+outs[2].Text != "日😀" {
		t.Fatal("combined text differs from input")
	}
}

func TestRawWriterFlushPreservesIncompleteUTF8(t *testing.T) {
	data := []byte{0xe6, 0x97}
	outs := collectRawOutputs(t, data)
	if len(outs) != 1 || outs[0].Text != string(data) {
		t.Fatalf("final flush lost bytes: %+v", outs)
	}
}

func newTestSlackOutputStream(interval time.Duration) (*slackOutputStream, chan *slackOutputEvent) {
	ch := make(chan *slackOutputEvent, 10)
	return newSlackOutputStream(ch, nil, false, command.ConversationID{}, command.MessageID{}, interval), ch
}

func receiveOutput(t *testing.T, ch <-chan *slackOutputEvent, timeout time.Duration) *slackOutputEvent {
	t.Helper()
	select {
	case output := <-ch:
		return output
	case <-time.After(timeout):
		t.Fatalf("timed out waiting for output after %s", timeout)
		return nil
	}
}

func waitForTimerCallback(t *testing.T, writer *slackOutputStream) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		writer.mu.Lock()
		pending := writer.timer != nil
		writer.mu.Unlock()
		if !pending {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("timed out waiting for timer callback")
}

func TestSlackOutputStreamFlushesAtBatchInterval(t *testing.T) {
	writer, ch := newTestSlackOutputStream(20 * time.Millisecond)
	if _, err := writer.Write([]byte("a")); err != nil {
		t.Fatal(err)
	}
	if output := receiveOutput(t, ch, time.Second); output.Text != "a" {
		t.Fatalf("output = %q, want %q", output.Text, "a")
	}
}

func TestSlackOutputStreamDoesNotResetTimerOnSubsequentWrite(t *testing.T) {
	writer, _ := newTestSlackOutputStream(time.Second)
	if _, err := writer.Write([]byte("a")); err != nil {
		t.Fatal(err)
	}
	writer.mu.Lock()
	firstTimer := writer.timer
	firstSequence := writer.timerSequence
	writer.mu.Unlock()
	if firstTimer == nil {
		t.Fatal("first write did not start a timer")
	}
	if _, err := writer.Write([]byte("b")); err != nil {
		t.Fatal(err)
	}
	writer.mu.Lock()
	timerWasReset := writer.timer != firstTimer || writer.timerSequence != firstSequence
	writer.mu.Unlock()
	if timerWasReset {
		t.Fatal("second write reset the batch timer")
	}
	if err := writer.Flush(); err != nil {
		t.Fatal(err)
	}
}

func TestSlackOutputStreamStartsNextBatchAfterTimerFlush(t *testing.T) {
	writer, ch := newTestSlackOutputStream(20 * time.Millisecond)
	if _, err := writer.Write([]byte("a")); err != nil {
		t.Fatal(err)
	}
	if output := receiveOutput(t, ch, time.Second); output.Text != "a" {
		t.Fatalf("first output = %q, want %q", output.Text, "a")
	}

	if _, err := writer.Write([]byte("b")); err != nil {
		t.Fatal(err)
	}
	if output := receiveOutput(t, ch, time.Second); output.Text != "b" {
		t.Fatalf("second output = %q, want %q", output.Text, "b")
	}
}

func TestSlackOutputStreamFlushesExplicitly(t *testing.T) {
	writer, ch := newTestSlackOutputStream(100 * time.Millisecond)
	if _, err := writer.Write([]byte("a")); err != nil {
		t.Fatal(err)
	}
	if err := writer.Flush(); err != nil {
		t.Fatal(err)
	}
	if output := receiveOutput(t, ch, time.Second); output.Text != "a" {
		t.Fatalf("output = %q, want %q", output.Text, "a")
	}
}

func TestSlackOutputStreamFlushesImmediatelyAtZeroInterval(t *testing.T) {
	writer, ch := newTestSlackOutputStream(0)
	if _, err := writer.Write([]byte("a")); err != nil {
		t.Fatal(err)
	}
	if output := receiveOutput(t, ch, time.Second); output.Text != "a" {
		t.Fatalf("output = %q, want %q", output.Text, "a")
	}
}

func TestSlackOutputStreamFlushesWhenBufferIsFull(t *testing.T) {
	writer, ch := newTestSlackOutputStream(time.Hour)
	data := make([]byte, 2049)
	for i := range data {
		data[i] = 'x'
	}
	if _, err := writer.Write(data); err != nil {
		t.Fatal(err)
	}
	if output := receiveOutput(t, ch, time.Second); output.Text != string(data) {
		t.Fatalf("output length = %d, want %d", len(output.Text), len(data))
	}
}

func TestSlackOutputStreamTimerFlushPreservesIncompleteSixel(t *testing.T) {
	writer, ch := newTestSlackOutputStream(20 * time.Millisecond)
	half := len(minimalRedSixel) / 2
	if _, err := writer.Write(minimalRedSixel[:half]); err != nil {
		t.Fatal(err)
	}
	waitForTimerCallback(t, writer)
	select {
	case output := <-ch:
		t.Fatalf("unexpected output from incomplete sixel: %+v", output)
	default:
	}

	if _, err := writer.Write(minimalRedSixel[half:]); err != nil {
		t.Fatal(err)
	}
	if err := writer.Flush(); err != nil {
		t.Fatal(err)
	}
	if output := receiveOutput(t, ch, time.Second); len(output.ImageData) == 0 {
		t.Fatal("final flush did not emit sixel image")
	}
}

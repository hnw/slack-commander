package cmd

import (
	"testing"
	"time"
)

func newTestOutputWriter(interval time.Duration) (*OutputWriter, chan *CommandOutput) {
	ch := make(chan *CommandOutput, 10)
	return newOutputWriter(ch, nil, false, ConversationID{}, MessageID{}, interval), ch
}

func receiveOutput(t *testing.T, ch <-chan *CommandOutput, timeout time.Duration) *CommandOutput {
	t.Helper()
	select {
	case output := <-ch:
		return output
	case <-time.After(timeout):
		t.Fatalf("timed out waiting for output after %s", timeout)
		return nil
	}
}

func waitForTimerCallback(t *testing.T, writer *OutputWriter) {
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

func TestOutputWriterFlushesAtBatchInterval(t *testing.T) {
	writer, ch := newTestOutputWriter(20 * time.Millisecond)
	if _, err := writer.Write([]byte("a")); err != nil {
		t.Fatal(err)
	}
	if output := receiveOutput(t, ch, time.Second); output.Text != "a" {
		t.Fatalf("output = %q, want %q", output.Text, "a")
	}
}

func TestOutputWriterDoesNotResetTimerOnSubsequentWrite(t *testing.T) {
	writer, _ := newTestOutputWriter(time.Second)
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

func TestOutputWriterStartsNextBatchAfterTimerFlush(t *testing.T) {
	writer, ch := newTestOutputWriter(20 * time.Millisecond)
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

func TestOutputWriterFlushesExplicitly(t *testing.T) {
	writer, ch := newTestOutputWriter(100 * time.Millisecond)
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

func TestOutputWriterFlushesImmediatelyAtZeroInterval(t *testing.T) {
	writer, ch := newTestOutputWriter(0)
	if _, err := writer.Write([]byte("a")); err != nil {
		t.Fatal(err)
	}
	if output := receiveOutput(t, ch, time.Second); output.Text != "a" {
		t.Fatalf("output = %q, want %q", output.Text, "a")
	}
}

func TestOutputWriterFlushesWhenBufferIsFull(t *testing.T) {
	writer, ch := newTestOutputWriter(time.Hour)
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

func TestOutputWriterTimerFlushPreservesIncompleteSixel(t *testing.T) {
	writer, ch := newTestOutputWriter(20 * time.Millisecond)
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

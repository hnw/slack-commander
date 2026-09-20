package cmd

import (
	"bytes"
	"context"
	"io"
	"testing"
	"time"
)

func TestExecLiveInput(t *testing.T) {
	for _, tt := range []struct {
		name, command, input, output string
	}{
		{"date", "date +done", "", "done\n"},
		{"one-line", "read -r line; printf '%s\\n' \"$line\"", "alpha\n", "alpha\n"},
		{"awk", "awk '{print; fflush(); if (NR == 2) exit}'", "alpha\nbeta\n", "alpha\nbeta\n"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			c := NewExecRunner().CommandContext(ctx, "/bin/sh", "-c", tt.command).(*execCmd)
			var out bytes.Buffer
			c.SetStdout(&out)
			started := false
			code := c.RunLive(0, func(stdin io.WriteCloser) func() {
				started = true
				if tt.input != "" {
					if _, err := io.WriteString(stdin, tt.input); err != nil {
						t.Error(err)
					}
				}
				return func() {}
			})
			if !started || code != 0 || out.String() != tt.output {
				t.Fatalf("started=%v code=%d output=%q", started, code, out.String())
			}
		})
	}
}

func TestExecLiveInputStartFailure(t *testing.T) {
	c := NewExecRunner().CommandContext(context.Background(), "/no-such-live-command").(*execCmd)
	code := c.RunLive(0, func(io.WriteCloser) func() {
		t.Error("published after failed Start")
		return func() {}
	})
	if code != 127 {
		t.Fatalf("code=%d", code)
	}
}

func TestExecLiveInputTimeout(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	c := NewExecRunner().CommandContext(ctx, "/usr/bin/wc", "-l").(*execCmd)
	var out bytes.Buffer
	c.SetStdout(&out)
	cleaned := false
	code := c.RunLive(1, func(stdin io.WriteCloser) func() {
		if _, err := io.WriteString(stdin, "alpha\n"); err != nil {
			t.Error(err)
		}
		return func() { cleaned = true }
	})
	if code != 143 || out.Len() != 0 || !cleaned {
		t.Fatalf("code=%d output=%q cleaned=%v", code, out.String(), cleaned)
	}
}

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
	} {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			c := NewExecRunner().CommandContext(ctx, "/bin/sh", "-c", tt.command).(*execCmd)
			var out bytes.Buffer
			c.SetStdout(&out)
			started := false
			code := c.RunLive(0, func(stdin io.WriteCloser) {
				started = true
				if tt.input != "" {
					if _, err := io.WriteString(stdin, tt.input); err != nil {
						t.Error(err)
					}
				}
			})
			if !started || code != 0 || out.String() != tt.output {
				t.Fatalf("started=%v code=%d output=%q", started, code, out.String())
			}
		})
	}
}

func TestExecLiveInputWaitsForEOF(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	c := NewExecRunner().CommandContext(ctx, "/usr/bin/wc", "-l").(*execCmd)
	var out bytes.Buffer
	c.SetStdout(&out)
	var input io.WriteCloser
	code := c.RunLive(1, func(stdin io.WriteCloser) {
		input = stdin
		if _, err := io.WriteString(stdin, "alpha\n"); err != nil {
			t.Error(err)
		}
	})
	if code != 143 || out.Len() != 0 {
		t.Fatalf("code=%d output=%q", code, out.String())
	}
	if input == nil {
		t.Fatal("not started")
	}
	if _, err := io.WriteString(input, "after exit\n"); err == nil {
		t.Fatal("stdin still writable after exit")
	}
}

type liveOutputChannel chan string

func (w liveOutputChannel) Write(data []byte) (int, error) {
	w <- string(data)
	return len(data), nil
}

func TestExecLiveInputProcessesSeparateAwkReplies(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c := NewExecRunner().CommandContext(ctx, "/usr/bin/awk", "{print; fflush(); if (NR == 2) exit}").(*execCmd)
	output := make(liveOutputChannel, 4)
	c.SetStdout(output)
	started := make(chan io.WriteCloser, 1)
	done := make(chan int, 1)
	go func() {
		done <- c.RunLive(0, func(stdin io.WriteCloser) {
			started <- stdin
		})
	}()
	var stdin io.WriteCloser
	select {
	case stdin = <-started:
	case <-ctx.Done():
		t.Fatal("not started")
	}
	for _, text := range []string{"alpha\n", "beta\n"} {
		if _, err := io.WriteString(stdin, text); err != nil {
			t.Fatal(err)
		}
		var got string
		for len(got) < len(text) {
			select {
			case part := <-output:
				got += part
			case <-ctx.Done():
				t.Fatal("missing incremental output")
			}
		}
		if got != text {
			t.Fatalf("got %q want %q", got, text)
		}
	}
	select {
	case code := <-done:
		if code != 0 {
			t.Fatalf("code=%d", code)
		}
	case <-ctx.Done():
		t.Fatal("did not finish")
	}
}

package cmd

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"
	"time"
)

func TestExecCmdSetEnvOverridesExistingSlackContext(t *testing.T) {
	command := NewExecRunner().CommandContext(context.Background(), "/usr/bin/env").(*execCmd)
	command.cmd.Env = []string{"SLACK_CHANNEL_ID=static"}
	command.SetEnv([]string{
		"SLACK_CHANNEL_ID=C123",
		"SLACK_THREAD_TS=1700000000.000100",
	})
	var output bytes.Buffer
	command.SetStdout(&output)
	if code := command.Run(0); code != 0 {
		t.Fatalf("code = %d", code)
	}
	if !strings.Contains(output.String(), "SLACK_CHANNEL_ID=C123\n") {
		t.Fatalf("channel environment = %q", output.String())
	}
	if strings.Contains(output.String(), "SLACK_CHANNEL_ID=static\n") {
		t.Fatalf("static channel environment survived: %q", output.String())
	}
	if !strings.Contains(output.String(), "SLACK_THREAD_TS=1700000000.000100\n") {
		t.Fatalf("thread environment = %q", output.String())
	}
}

func TestExecStdin(t *testing.T) {
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
			code := c.RunWithStdin(0, func(stdin io.WriteCloser) {
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

func TestExecTTYProvidesTerminalAndLiveInput(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c := NewExecRunner().CommandContext(
		ctx,
		"/bin/sh",
		"-c",
		"test -t 0 && test -t 1 && test -t 2; IFS= read -r line; printf 'tty:%s' \"$line\"",
	).(*execCmd)
	c.SetTTY()
	var out bytes.Buffer
	c.SetStdout(&out)
	if code := c.RunWithStdin(0, func(stdin io.WriteCloser) {
		if _, err := io.WriteString(stdin, "input\n"); err != nil {
			t.Error(err)
		}
	}); code != 0 {
		t.Fatalf("code=%d output=%q", code, out.String())
	}
	if !strings.Contains(out.String(), "tty:input") {
		t.Fatalf("output=%q", out.String())
	}
}

func TestExecTTYInitialInputWithoutConversationKeepsOutputOpen(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c := NewExecRunner().CommandContext(
		ctx,
		"/bin/sh",
		"-c",
		"IFS= read -r line; sleep 0.1; printf 'done:%s' \"$line\"",
	).(*execCmd)
	c.SetTTY()
	var out bytes.Buffer
	c.SetStdout(&out)
	if code := runWithInput(c, 0, 0, "initial\n", ConversationContext{}, nil); code != 0 {
		t.Fatalf("code=%d output=%q", code, out.String())
	}
	if !strings.Contains(out.String(), "done:initial") {
		t.Fatalf("output=%q", out.String())
	}
}

func TestExecTTYSessionCloseDoesNotCloseTerminal(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c := NewExecRunner().CommandContext(
		ctx,
		"/bin/sh",
		"-c",
		"IFS= read -r line; printf ready; sleep 0.1; printf ':done:%s' \"$line\"",
	).(*execCmd)
	c.SetTTY()
	output := make(stdinOutputChannel, 10)
	c.SetStdout(output)
	sessionReady := make(chan *InteractiveStdin, 1)
	done := make(chan int, 1)
	go func() {
		done <- c.RunWithStdin(0, func(stdin io.WriteCloser) {
			session := NewInteractiveStdin(stdin, "initial", func(error) {})
			sessionReady <- session
		})
	}()

	session := <-sessionReady
	var text string
	for !strings.Contains(text, "ready") {
		select {
		case part := <-output:
			text += part
		case <-ctx.Done():
			t.Fatal("TTY command did not receive initial input")
		}
	}
	session.Close()
	select {
	case code := <-done:
		if code != 0 {
			t.Fatalf("code=%d output=%q", code, text)
		}
	case <-ctx.Done():
		t.Fatal("TTY command did not finish")
	}
	for len(output) > 0 {
		text += <-output
	}
	if !strings.Contains(text, ":done:initial") {
		t.Fatalf("output=%q", text)
	}
}

func TestExecTTYClosesMasterAfterProcessExit(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c := NewExecRunner().CommandContext(ctx, "/bin/sh", "-c", "printf done").(*execCmd)
	c.SetTTY()
	writerReady := make(chan io.WriteCloser, 1)
	if code := c.RunWithStdin(0, func(stdin io.WriteCloser) { writerReady <- stdin }); code != 0 {
		t.Fatalf("code=%d", code)
	}
	writer := <-writerReady
	if _, err := io.WriteString(writer, "late"); err == nil {
		t.Fatal("TTY master remained writable after process exit")
	}
}

func TestExecTTYCancellationStopsProcessGroup(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	c := NewExecRunner().CommandContext(ctx, "/bin/sh", "-c", "sleep 30 & wait").(*execCmd)
	c.SetTTY()
	done := make(chan int, 1)
	go func() { done <- c.RunWithStdin(0, nil) }()
	time.Sleep(50 * time.Millisecond)
	cancel()
	select {
	case code := <-done:
		if code != 143 {
			t.Fatalf("code=%d", code)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("TTY process group was not cancelled")
	}
}

func TestExecStdinWaitsForEOF(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	c := NewExecRunner().CommandContext(ctx, "/usr/bin/wc", "-l").(*execCmd)
	var out bytes.Buffer
	c.SetStdout(&out)
	var input io.WriteCloser
	code := c.RunWithStdin(1, func(stdin io.WriteCloser) {
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

type stdinOutputChannel chan string

func (w stdinOutputChannel) Write(data []byte) (int, error) {
	w <- string(data)
	return len(data), nil
}

func TestExecStdinProcessesSeparateAwkReplies(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c := NewExecRunner().CommandContext(ctx, "/usr/bin/awk", "{print; fflush(); if (NR == 2) exit}").(*execCmd)
	output := make(stdinOutputChannel, 4)
	c.SetStdout(output)
	started := make(chan io.WriteCloser, 1)
	done := make(chan int, 1)
	go func() {
		done <- c.RunWithStdin(0, func(stdin io.WriteCloser) {
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

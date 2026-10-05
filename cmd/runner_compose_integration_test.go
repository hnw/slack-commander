package cmd

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestComposeRunTransfersStreamsAndExitCode(t *testing.T) {
	if os.Getenv("SLACK_COMMANDER_COMPOSE_INTEGRATION") != "1" {
		t.Skip("set SLACK_COMMANDER_COMPOSE_INTEGRATION=1 to run with Docker")
	}
	dir := t.TempDir()
	if err := os.WriteFile(
		filepath.Join(dir, "compose.yaml"),
		[]byte("services:\n  app:\n    image: busybox:1.36\n"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	for _, tt := range []struct {
		name     string
		command  []string
		wantCode int
		wantOut  string
		wantErr  string
	}{
		{
			name:     "success",
			command:  []string{"/bin/sh", "-c", "printf 'out'; printf 'err' >&2"},
			wantCode: 0,
			wantOut:  "out",
			wantErr:  "err",
		},
		{
			name:     "non zero exit",
			command:  []string{"/bin/sh", "-c", "printf 'out'; exit 3"},
			wantCode: 3,
			wantOut:  "out",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			command := NewComposeRunner(dir).CommandContext(ctx, "app", tt.command...)
			var stdout, stderr bytes.Buffer
			command.SetStdout(&stdout)
			command.SetStderr(&stderr)
			if code := command.Run(); code != tt.wantCode {
				t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
			}
			if got := stdout.String(); got != tt.wantOut {
				t.Fatalf("stdout=%q", got)
			}
			if got := stderr.String(); !strings.Contains(got, tt.wantErr) {
				t.Fatalf("stderr=%q", got)
			}
		})
	}
}

func TestComposeStdinForwardsInitialAndReply(t *testing.T) {
	if os.Getenv("SLACK_COMMANDER_COMPOSE_INTEGRATION") != "1" {
		t.Skip("set SLACK_COMMANDER_COMPOSE_INTEGRATION=1 to run with Docker")
	}
	dir := t.TempDir()
	composeFile := []byte("services:\n  app:\n    image: busybox:1.36\n")
	if err := os.WriteFile(filepath.Join(dir, "compose.yaml"), composeFile, 0o600); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var registry testThreadRegistry
	key := ConversationID{ChannelID: "C", RootTimestamp: "1"}
	command := NewComposeRunner(dir).CommandContext(
		ctx,
		"app",
		"/bin/sh",
		"-c",
		"IFS= read -r first; IFS= read -r second; printf '%s|%s\\n' \"$first\" \"$second\"",
	)
	var output bytes.Buffer
	var stderr bytes.Buffer
	command.SetStdout(&output)
	command.SetStderr(&stderr)
	done := make(chan int, 1)
	go func() {
		done <- testRunWithInput(
			command,
			0,
			"initial",
			ConversationID{
				ChannelID:     key.ChannelID,
				RootTimestamp: key.RootTimestamp,
			},
			&registry,
		)
	}()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	var endpoint *InteractiveStdin
	for endpoint == nil {
		endpoint = registry.lookup(key)
		if endpoint != nil {
			break
		}
		select {
		case code := <-done:
			t.Fatalf(
				"compose exited before stdin was published: code=%d stderr=%q",
				code,
				stderr.String(),
			)
		case <-ticker.C:
		case <-ctx.Done():
			t.Fatalf("interactive stdin was not published: stderr=%q", stderr.String())
		}
	}
	if err := endpoint.TrySend("reply"); err != nil {
		t.Fatal(err)
	}
	select {
	case code := <-done:
		if code != 0 || output.String() != "initial|reply\n" {
			t.Fatalf("code=%d output=%q", code, output.String())
		}
	case <-ctx.Done():
		t.Fatal("compose command did not finish")
	}
	if registry.lookup(key) != nil {
		t.Fatal("registry entry survived compose exit")
	}
}

func TestComposeTTYProvidesTerminalAndMergedStream(t *testing.T) {
	if os.Getenv("SLACK_COMMANDER_COMPOSE_INTEGRATION") != "1" {
		t.Skip("set SLACK_COMMANDER_COMPOSE_INTEGRATION=1 to run with Docker")
	}
	dir := t.TempDir()
	if err := os.WriteFile(
		filepath.Join(dir, "compose.yaml"),
		[]byte("services:\n  app:\n    image: busybox:1.36\n"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	command := NewComposeRunner(dir).CommandContext(
		ctx,
		"app",
		"/bin/sh",
		"-c",
		"test -t 0 && test -t 1 && test -t 2; IFS= read -r line; printf 'tty:%s' \"$line\"; printf ':stderr' >&2",
	)
	command.(interface{ SetTTY() }).SetTTY()
	var output bytes.Buffer
	command.SetStdout(&output)
	command.SetStderr(&output)
	if code := command.(interface {
		RunWithStdin(func(io.WriteCloser)) int
	}).RunWithStdin(func(stdin io.WriteCloser) {
		if _, err := io.WriteString(stdin, "input\n"); err != nil {
			t.Error(err)
		}
	}); code != 0 {
		t.Fatalf("code=%d output=%q", code, output.String())
	}
	if got := output.String(); !strings.Contains(got, "tty:input:stderr") {
		t.Fatalf("output=%q", got)
	}
}

func TestComposeTTYCancellationRemovesThreadInput(t *testing.T) {
	if os.Getenv("SLACK_COMMANDER_COMPOSE_INTEGRATION") != "1" {
		t.Skip("set SLACK_COMMANDER_COMPOSE_INTEGRATION=1 to run with Docker")
	}
	dir := t.TempDir()
	if err := os.WriteFile(
		filepath.Join(dir, "compose.yaml"),
		[]byte("services:\n  app:\n    image: busybox:1.36\n"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var registry testThreadRegistry
	key := ConversationID{ChannelID: "C", RootTimestamp: "1"}
	command := NewComposeRunner(dir).CommandContext(ctx, "app", "/bin/sh", "-c", "sleep 30")
	command.(interface{ SetTTY() }).SetTTY()
	var output bytes.Buffer
	command.SetStdout(&output)
	command.SetStderr(&output)
	done := make(chan int, 1)
	go func() {
		done <- testRunWithInput(
			command,
			0,
			"",
			ConversationID(key),
			&registry,
		)
	}()
	_ = waitForInteractiveStdin(t, &registry, key)
	cancel()
	select {
	case code := <-done:
		if code != 143 {
			t.Fatalf("code=%d output=%q", code, output.String())
		}
	case <-time.After(10 * time.Second):
		t.Fatal("TTY compose command was not cancelled")
	}
	if registry.lookup(key) != nil {
		t.Fatal("TTY compose endpoint survived cancellation")
	}
}

func TestComposeStdinIdleEOF(t *testing.T) {
	if os.Getenv("SLACK_COMMANDER_COMPOSE_INTEGRATION") != "1" {
		t.Skip("set SLACK_COMMANDER_COMPOSE_INTEGRATION=1 to run with Docker")
	}
	dir := t.TempDir()
	composeFile := []byte("services:\n  app:\n    image: busybox:1.36\n")
	if err := os.WriteFile(filepath.Join(dir, "compose.yaml"), composeFile, 0o600); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var registry testThreadRegistry
	key := ConversationID{ChannelID: "C", RootTimestamp: "1"}
	command := NewComposeRunner(dir).CommandContext(ctx, "app", "wc", "-l")
	var output bytes.Buffer
	command.SetStdout(&output)
	done := make(chan int, 1)
	go func() {
		done <- testRunWithInput(
			command,
			time.Second,
			"initial",
			ConversationID{
				ChannelID:     key.ChannelID,
				RootTimestamp: key.RootTimestamp,
			},
			&registry,
		)
	}()
	select {
	case code := <-done:
		if code != 0 || output.String() != "1\n" {
			t.Fatalf("code=%d output=%q", code, output.String())
		}
	case <-ctx.Done():
		t.Fatal("compose command did not receive idle EOF")
	}
	if registry.lookup(key) != nil {
		t.Fatal("registry entry survived idle EOF")
	}
}

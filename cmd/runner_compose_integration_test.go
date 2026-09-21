package cmd

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

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
	var registry ThreadInputRegistry
	key := ThreadKey{ChannelID: "C", RootThreadTimestamp: "1"}
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
		done <- runWithInput(
			command,
			0,
			0,
			"initial",
			//nolint:staticcheck // ConversationContext may gain fields independently of ThreadKey.
			ConversationContext{
				ChannelID:           key.ChannelID,
				RootThreadTimestamp: key.RootThreadTimestamp,
			},
			&registry,
		)
	}()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	var endpoint *InteractiveStdin
	for endpoint == nil {
		endpoint = registry.Lookup(key)
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
	if registry.Lookup(key) != nil {
		t.Fatal("registry entry survived compose exit")
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
	var registry ThreadInputRegistry
	key := ThreadKey{ChannelID: "C", RootThreadTimestamp: "1"}
	command := NewComposeRunner(dir).CommandContext(ctx, "app", "wc", "-l")
	var output bytes.Buffer
	command.SetStdout(&output)
	done := make(chan int, 1)
	go func() {
		done <- runWithInput(
			command,
			0,
			time.Second,
			"initial",
			//nolint:staticcheck // ConversationContext may gain fields independently of ThreadKey.
			ConversationContext{
				ChannelID:           key.ChannelID,
				RootThreadTimestamp: key.RootThreadTimestamp,
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
	if registry.Lookup(key) != nil {
		t.Fatal("registry entry survived idle EOF")
	}
}

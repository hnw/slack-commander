package main

import (
	"context"
	"io"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hnw/slack-commander/cmd"
)

func TestRunCheckConfig(t *testing.T) {
	if got := run([]string{"--help"}); got != 0 {
		t.Fatalf("run(--help) = %d, want 0", got)
	}

	valid := writeConfigFile(t, "slack_bot_token = 'xoxb-test'\nslack_app_token = 'xapp-test'\nallowed_user_ids = ['U']\n[[commands]]\nkeyword = 'date'\ncommand = 'date'")
	if got := run([]string{"--check-config", "--config-file", valid}); got != 0 {
		t.Fatalf("run(valid config) = %d, want 0", got)
	}

	missingKeyword := writeConfigFile(t, "slack_bot_token = 'xoxb-test'\nslack_app_token = 'xapp-test'\nallowed_user_ids = ['U']\n[[commands]]\ncommand = 'date'")
	var got int
	originalStderr := os.Stderr
	stderr := captureStderr(t, func() {
		got = run([]string{"--check-config", "--config-file", missingKeyword})
	})
	if os.Stderr != originalStderr {
		t.Fatal("captureStderr() did not restore os.Stderr")
	}
	if got == 0 {
		t.Fatal("run(config without keyword) = 0, want non-zero")
	}
	if stderr != "keyword is required\n" {
		t.Fatalf("run(config without keyword) stderr = %q, want %q", stderr, "keyword is required\n")
	}

	invalid := writeConfigFile(t, "unknown = true")
	stderr = captureStderr(t, func() {
		got = run([]string{"--check-config", "--config-file", invalid})
	})
	if got == 0 {
		t.Fatal("run(invalid config) = 0, want non-zero")
	}
	if !strings.Contains(stderr, "1| unknown = true") {
		t.Fatalf("run(invalid config) stderr = %q, want source context", stderr)
	}
	if !strings.Contains(stderr, "unknown field") {
		t.Fatalf("run(invalid config) stderr = %q, want unknown field error", stderr)
	}

	stderr = captureStderr(t, func() {
		got = run([]string{"--config-file", missingKeyword})
	})
	if got == 0 {
		t.Fatal("run(config without keyword) = 0, want non-zero")
	}
	if stderr != "keyword is required\n" {
		t.Fatalf("run(config without keyword) stderr = %q, want %q", stderr, "keyword is required\n")
	}
}

func TestStartWorkersExitWhenQueueClosesOrContextCancels(t *testing.T) {
	for _, closeQueue := range []bool{true, false} {
		t.Run(map[bool]string{true: "queue closes", false: "context cancels"}[closeQueue], func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			inputs := make(chan *cmd.CommandInput)
			var workers sync.WaitGroup
			startWorkers(ctx, 2, inputs, nil, nil, nil, &workers)
			if closeQueue {
				close(inputs)
			} else {
				cancel()
			}
			done := make(chan struct{})
			go func() { workers.Wait(); close(done) }()
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("workers did not exit")
			}
		})
	}
}

func TestRunnerFactoryScopesComposeRunnerToFactory(t *testing.T) {
	firstFactory := newRunnerFactory()
	secondFactory := newRunnerFactory()
	config := &cmd.ExecutionConfig{Runner: cmd.RunnerCompose}
	first := firstFactory(config)
	sameWorker := firstFactory(config)
	otherWorker := secondFactory(config)
	if first != sameWorker {
		t.Fatal("compose runner was not reused within factory")
	}
	if first == otherWorker {
		t.Fatal("compose runner was shared across factories")
	}
}

func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	previous := os.Stderr
	os.Stderr = writer
	defer func() {
		os.Stderr = previous
	}()

	fn()
	os.Stderr = previous
	if closeErr := writer.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	output, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	if closeErr := reader.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	return string(output)
}

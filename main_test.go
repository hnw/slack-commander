package main

import (
	"context"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hnw/slack-commander/cmd"
)

type listenerFailureTransport struct {
	response string
}

func (transport listenerFailureTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if request.URL.Path == "/api/auth.test" {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(transport.response)),
			Header:     make(http.Header),
		}, nil
	}
	<-request.Context().Done()
	return nil, request.Context().Err()
}

func TestRunStopsWhenSlackIdentityCannotBeEstablished(t *testing.T) {
	config := writeConfigFile(t, "slack_bot_token = 'xoxb-test'\nslack_app_token = 'xapp-test'\nallowed_user_ids = ['U']")
	for _, response := range []string{
		`{"ok":false,"error":"invalid_auth"}`,
		`{"ok":true,"bot_id":"B-self"}`,
		`{"ok":true,"user_id":"U-self"}`,
	} {
		t.Run(response, func(t *testing.T) {
			previousClient := http.DefaultClient
			http.DefaultClient = &http.Client{Transport: listenerFailureTransport{response: response}}
			t.Cleanup(func() { http.DefaultClient = previousClient })
			var exitCode int
			stderr := captureStderr(t, func() { exitCode = run([]string{"--config-file", config}) })
			if exitCode != 1 || !strings.Contains(stderr, "Slack listener error") {
				t.Fatalf("exitCode = %d, stderr = %q, want listener error and exit 1", exitCode, stderr)
			}
		})
	}
}

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

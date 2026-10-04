package cmd

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestExecDeadlineReturns143WithoutTimeoutMessage(t *testing.T) {
	for _, tty := range []bool{false, true} {
		t.Run(map[bool]string{false: "plain", true: "tty"}[tty], func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer cancel()
			command := NewExecRunner().CommandContext(ctx, "/bin/sh", "-c", "sleep 10").(*execCmd)
			if tty {
				command.SetTTY()
			}
			var stderr bytes.Buffer
			command.SetStderr(&stderr)
			if code := command.Run(); code != 143 {
				t.Fatalf("code = %d, want 143", code)
			}
			if stderr.Len() != 0 {
				t.Fatalf("stderr = %q, want empty", stderr.String())
			}
		})
	}
}

func TestComposeContextTerminationReturns143WithoutTimeoutMessage(t *testing.T) {
	for _, deadline := range []bool{false, true} {
		t.Run(map[bool]string{false: "cancel", true: "deadline"}[deadline], func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			if deadline {
				cancel()
				ctx, cancel = context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
			}
			defer cancel()
			if !deadline {
				cancel()
			}
			var stderr bytes.Buffer
			command := &composeCmd{ctx: ctx, stderr: &stderr}
			if code := command.finish(ctx.Err()); code != 143 {
				t.Fatalf("code = %d, want 143", code)
			}
			if stderr.Len() != 0 {
				t.Fatalf("stderr = %q, want empty", stderr.String())
			}
		})
	}
}

func TestHTTPDeadlineReturns143WithoutTimeoutMessage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, request *http.Request) {
		<-request.Context().Done()
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	command := NewHTTPRunner(RunnerConfig{RawRunnerConfig: RawRunnerConfig{URL: server.URL}}).CommandContext(ctx, "test")
	var stderr bytes.Buffer
	command.SetStderr(&stderr)
	if code := command.Run(); code != 143 {
		t.Fatalf("code = %d, want 143", code)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
}

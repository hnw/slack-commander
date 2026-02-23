package cmd

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHTTPRunnerPostWithWildcard(t *testing.T) {
	type requestResult struct {
		method      string
		path        string
		contentType string
		body        string
		err         error
	}
	resultCh := make(chan requestResult, 1)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bodyBytes, err := io.ReadAll(r.Body)
		resultCh <- requestResult{
			method:      r.Method,
			path:        r.URL.Path,
			contentType: r.Header.Get("Content-Type"),
			body:        string(bodyBytes),
			err:         err,
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()

	cfg := NewCommandConfig(&Definition{
		Runner:  "http",
		Method:  "post",
		URL:     srv.URL + "/hook",
		Headers: map[string]string{"Content-Type": "application/json"},
		Body:    `{"text":"*"}`,
	}, nil)

	runner := NewHTTPRunner(cfg)
	cmd := runner.CommandContext(context.Background(), "http", "hello world")
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.SetStdout(&stdout)
	cmd.SetStderr(&stderr)

	exitCode := cmd.Run(0)
	if exitCode != 0 {
		t.Fatalf("expected exit code 0, got %d", exitCode)
	}
	result := <-resultCh
	if result.err != nil {
		t.Fatalf("failed to read body: %v", result.err)
	}
	if stderr.String() != "" {
		t.Fatalf("unexpected stderr: %q", stderr.String())
	}
	if stdout.String() != "ok" {
		t.Fatalf("unexpected stdout: %q", stdout.String())
	}
	if result.method != "POST" {
		t.Fatalf("expected method POST, got %q", result.method)
	}
	if result.path != "/hook" {
		t.Fatalf("expected path /hook, got %q", result.path)
	}
	if result.contentType != "application/json" {
		t.Fatalf("expected Content-Type application/json, got %q", result.contentType)
	}
	if result.body != `{"text":"hello world"}` {
		t.Fatalf("unexpected body: %q", result.body)
	}
}

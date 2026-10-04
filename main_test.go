package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hnw/slack-commander/cmd"
	"github.com/hnw/slack-commander/pubsub"
	"github.com/slack-go/slack"
	"github.com/slack-go/slack/socketmode"
)

type listenerFailureTransport struct {
	response string
}

func TestShutdownDrainsAsyncHTTPOutputThroughSlackWriter(t *testing.T) {
	type slackRequest struct {
		path string
		form map[string][]string
	}
	requests := make(chan slackRequest, 4)
	httpStarted := make(chan struct{}, 1)
	releaseHTTP := make(chan struct{})
	var releaseOnce sync.Once
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			httpStarted <- struct{}{}
			<-releaseHTTP
			_, _ = io.WriteString(w, "async-result")
			return
		}
		if err := r.ParseForm(); err != nil {
			t.Errorf("ParseForm() error = %v", err)
		}
		requests <- slackRequest{path: r.URL.Path, form: r.Form}
		_, _ = io.WriteString(w, `{"ok":true,"channel":"C123","ts":"1700000000.000300"}`)
	}))
	t.Cleanup(server.Close)

	outputs := make(chan *cmd.CommandOutput, 10)
	httpConfig := cmd.RunnerConfig{RawRunnerConfig: cmd.RawRunnerConfig{Runner: cmd.RunnerHTTP, Method: "GET", URL: server.URL}}
	command := cmd.NewCommand(cmd.CommandConfig{
		Index:         0,
		MatcherConfig: cmd.MatcherConfig{RawMatcherConfig: cmd.RawMatcherConfig{Keyword: "lookup"}},
		RunnerConfig:  httpConfig,
		Dispatch:      cmd.DispatchExecutor,
	}, cmd.NewHTTPRunner(httpConfig), nil)
	commands := cmd.NewCommandSet([]*cmd.Command{command})
	commands.ConfigureOutput(outputs)
	executor := cmd.NewExecutor()
	dispatchCtx, cancelDispatch := context.WithCancel(context.Background())
	dispatcher := cmd.NewCommandDispatcher(dispatchCtx, executor, &cmd.StdinStore{}, &cmd.ConversationLocks{}, nil)
	smc := socketmode.New(slack.New("token", slack.OptionAPIURL(server.URL+"/")))
	writerDone := make(chan struct{})
	writerCtx, cancelWriter := context.WithCancel(context.Background())
	var outputCloseOnce sync.Once
	go func() {
		pubsub.SlackWriter(writerCtx, smc, outputs)
		close(writerDone)
	}()
	t.Cleanup(func() {
		dispatcher.Close()
		releaseOnce.Do(func() { close(releaseHTTP) })
		cancelDispatch()
		cancelWriter()
		dispatcher.Wait()
		outputCloseOnce.Do(func() { close(outputs) })
		<-writerDone
	})

	input := &cmd.CommandInput{
		Text:                  "lookup",
		ConversationID:        cmd.ConversationID{ChannelID: "C123", RootTimestamp: "1700000000.000100"},
		MessageID:             cmd.MessageID{ChannelID: "C123", Timestamp: "1700000000.000200"},
		AllowedCommandIndexes: []int{0},
	}
	input.ResolvedInput = commands.ResolveInput(input.Text, input.AllowedCommandIndexes)
	if result := dispatcher.Dispatch(input); result != cmd.DispatchAccepted {
		t.Fatalf("Dispatch() = %v, want accepted", result)
	}
	select {
	case <-httpStarted:
	case <-time.After(3 * time.Second):
		t.Fatal("asynchronous HTTP request did not start")
	}
	dispatcher.Close()
	cancelWriter()
	releaseOnce.Do(func() { close(releaseHTTP) })
	dispatcher.Wait()
	outputCloseOnce.Do(func() { close(outputs) })
	select {
	case <-writerDone:
	case <-time.After(3 * time.Second):
		t.Fatal("SlackWriter did not drain output queue")
	}

	var reactionNames []string
	postedBody := false
	var paths []string
	for len(requests) > 0 {
		request := <-requests
		paths = append(paths, request.path)
		switch request.path {
		case "/reactions.add", "/reactions.remove":
			reactionNames = append(reactionNames, request.form["name"][0])
		case "/chat.postMessage":
			encoded, _ := json.Marshal(request.form)
			postedBody = strings.Contains(string(encoded), "async-result")
		}
	}
	if !slices.Contains(reactionNames, "white_check_mark") || !slices.Contains(reactionNames, "eyes") {
		t.Fatalf("reaction names = %v, paths = %v, want running and finished reactions", reactionNames, paths)
	}
	if !postedBody {
		t.Fatal("SlackWriter did not post the asynchronous HTTP response body")
	}
}

func TestParseErrorUsesSlackOutputPipelineWithoutLifecycleReactions(t *testing.T) {
	for _, tc := range []struct {
		name      string
		dispatch  cmd.DispatchMode
		rootTS    string
		msgTS     string
		broadcast bool
	}{
		{name: "root", dispatch: cmd.DispatchQueue, rootTS: "1700000000.000100", msgTS: "1700000000.000100"},
		{name: "reply", dispatch: cmd.DispatchExecutor, rootTS: "1700000000.000100", msgTS: "1700000000.000200", broadcast: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			type slackRequest struct {
				path string
				form url.Values
			}
			requests := make(chan slackRequest, 4)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if err := r.ParseForm(); err != nil {
					t.Errorf("ParseForm() error = %v", err)
				}
				requests <- slackRequest{path: r.URL.Path, form: r.Form}
				_, _ = io.WriteString(w, `{"ok":true,"channel":"C123","ts":"1700000000.000300"}`)
			}))
			t.Cleanup(server.Close)

			outputs := make(chan *cmd.CommandOutput, 1)
			broadcast := tc.broadcast
			command := cmd.NewCommand(cmd.CommandConfig{
				Dispatch:          tc.dispatch,
				SystemReplyConfig: pubsub.NewSystemReplyConfig(&broadcast),
			}, nil, nil)
			cmd.NewCommandSet([]*cmd.Command{command}).ConfigureOutput(outputs)
			dispatcher := newMainTestDispatcher(context.Background(), nil, nil, func(*cmd.CommandInput) bool {
				t.Fatal("parse error entered the command queue")
				return false
			})
			input := &cmd.CommandInput{
				ConversationID: cmd.ConversationID{ChannelID: "C123", RootTimestamp: tc.rootTS},
				MessageID:      cmd.MessageID{ChannelID: "C123", Timestamp: tc.msgTS},
				ResolvedInput: &cmd.ResolvedInput{
					Commands: []cmd.ResolvedCommand{{Command: command}},
					ParseErr: errors.New("Parse error: malformed input"),
				},
			}
			if got := dispatcher.Dispatch(input); got != cmd.DispatchAccepted {
				t.Fatalf("Dispatch() = %v, want accepted", got)
			}
			dispatcher.Close()
			dispatcher.Wait()
			close(outputs)

			smc := socketmode.New(slack.New("token", slack.OptionAPIURL(server.URL+"/")))
			writerDone := make(chan struct{})
			go func() { pubsub.SlackWriter(context.Background(), smc, outputs); close(writerDone) }()
			select {
			case <-writerDone:
			case <-time.After(3 * time.Second):
				t.Fatal("SlackWriter did not finish")
			}
			if len(requests) != 1 {
				t.Fatalf("Slack API requests = %d, want one text post and no reactions", len(requests))
			}
			request := <-requests
			wantBroadcast := ""
			if tc.broadcast {
				wantBroadcast = strconv.FormatBool(tc.broadcast)
			}
			if request.path != "/chat.postMessage" || request.form.Get("channel") != "C123" || request.form.Get("thread_ts") != tc.rootTS || request.form.Get("reply_broadcast") != wantBroadcast {
				t.Fatalf("Slack request = %#v, want configured thread text post", request)
			}
			if !strings.Contains(request.form.Get("attachments"), "Parse error: malformed input") {
				t.Fatalf("posted attachments = %q, want parse error text", request.form.Get("attachments"))
			}
		})
	}
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

func TestRunVersionSkipsConfig(t *testing.T) {
	missing := t.TempDir() + "/missing.toml"
	if got := run([]string{"--version", "--config-file", missing}); got != 0 {
		t.Fatalf("run(--version) = %d, want 0", got)
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
	if stderr != "invalid configuration:\n  - command #1: keyword is required\n" {
		t.Fatalf("run(config without keyword) stderr = %q, want formatted validation error", stderr)
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
	if strings.Contains(stderr, "invalid configuration:") {
		t.Fatalf("decode error has validation heading: %q", stderr)
	}

	stderr = captureStderr(t, func() {
		got = run([]string{"--config-file", missingKeyword})
	})
	if got == 0 {
		t.Fatal("run(config without keyword) = 0, want non-zero")
	}
	if stderr != "invalid configuration:\n  - command #1: keyword is required\n" {
		t.Fatalf("run(config without keyword) stderr = %q, want formatted validation error", stderr)
	}
}

func newMainTestDispatcher(
	ctx context.Context,
	stdinStore *cmd.StdinStore,
	conversationLocks *cmd.ConversationLocks,
	enqueue func(*cmd.CommandInput) bool,
) *cmd.CommandDispatcher {
	if stdinStore == nil {
		stdinStore = &cmd.StdinStore{}
	}
	if conversationLocks == nil {
		conversationLocks = &cmd.ConversationLocks{}
	}
	return cmd.NewCommandDispatcher(ctx, cmd.NewExecutor(), stdinStore, conversationLocks, enqueue)
}

func TestStartWorkersExitWhenQueueClosesOrContextCancels(t *testing.T) {
	for _, closeQueue := range []bool{true, false} {
		t.Run(map[bool]string{true: "queue closes", false: "context cancels"}[closeQueue], func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			inputs := make(chan *cmd.CommandInput)
			var workers sync.WaitGroup
			startWorkers(ctx, 2, inputs, nil, nil, cmd.NewExecutor(), &workers)
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

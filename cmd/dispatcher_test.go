package cmd

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestAsyncHTTPUsesExecutorOutputPipelineForFailuresAndCancellation(t *testing.T) {
	started := make(chan string, 3)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/slow" || r.URL.Path == "/cancel" {
			started <- r.URL.Path
			<-r.Context().Done()
			return
		}
		if r.URL.Path == "/failure" {
			w.WriteHeader(http.StatusBadGateway)
			_, _ = io.WriteString(w, "gateway failed")
			return
		}
		_, _ = io.WriteString(w, "ok")
	}))
	defer server.Close()
	closedServer := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	closedURL := closedServer.URL
	closedServer.Close()

	tests := []struct {
		name         string
		url          string
		timeout      int
		cancel       bool
		startedPath  string
		wantText     string
		wantExitCode int
		wantErrOut   bool
	}{
		{name: "non-2xx", url: server.URL + "/failure", wantText: "gateway failed", wantExitCode: 1, wantErrOut: true},
		{name: "request error", url: closedURL, wantText: "Error:", wantExitCode: 127, wantErrOut: true},
		{name: "timeout", url: server.URL + "/slow", timeout: 1, startedPath: "/slow", wantText: "Timeout exceeded (1s)", wantExitCode: 143, wantErrOut: true},
		{name: "context cancellation", url: server.URL + "/cancel", cancel: true, startedPath: "/cancel", wantExitCode: 143},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			outputs := make(chan *CommandOutput, 20)
			config := RunnerConfig{RawRunnerConfig: RawRunnerConfig{Runner: RunnerHTTP, Method: "GET", URL: tc.url}}
			command := NewCommand(CommandConfig{
				Index:         0,
				MatcherConfig: MatcherConfig{RawMatcherConfig: RawMatcherConfig{Keyword: "lookup"}},
				RunnerConfig:  config,
				ExecutorConfig: ExecutorConfig{
					Timeout: tc.timeout,
				},
			}, NewHTTPRunner(config), nil)
			commands := NewCommandSet([]*Command{command})
			dispatcher := NewCommandDispatcher(ctx, NewExecutor(commands, outputs), &StdinStore{}, &ConversationLocks{}, nil)
			router := NewConversationRouterWithRootInputResolver(commands, nil, dispatcher, 1)
			conversation := ConversationID{ChannelID: "C", RootTimestamp: "1"}
			if result, err := router.Accept(&CommandInput{Text: "lookup", ConversationID: conversation, MessageID: MessageID{Timestamp: "1"}, AllowedCommandIndexes: []int{0}}); err != nil || result != AcceptRouted {
				t.Fatalf("Accept() = %v, %v; want routed", result, err)
			}
			if tc.startedPath != "" {
				select {
				case got := <-started:
					if got != tc.startedPath {
						t.Fatalf("request path = %q, want %q", got, tc.startedPath)
					}
				case <-time.After(3 * time.Second):
					t.Fatalf("request %q did not start", tc.startedPath)
				}
			}
			if tc.cancel {
				cancel()
			}
			dispatcher.Close()
			dispatcher.Wait()
			assertHTTPFailureOutput(t, outputs, tc.wantExitCode, tc.wantErrOut, tc.wantText)
		})
	}
}

func assertHTTPFailureOutput(t *testing.T, outputs chan *CommandOutput, wantExitCode int, wantErrOut bool, wantText string) {
	t.Helper()
	var gotText strings.Builder
	spawned, finished, errOut := false, false, false
	for len(outputs) > 0 {
		output := <-outputs
		gotText.WriteString(output.Text)
		spawned = spawned || output.Spawned
		errOut = errOut || output.IsErrOut
		if output.Finished {
			finished = true
			if output.ExitCode != wantExitCode {
				t.Fatalf("exit code = %d, want %d", output.ExitCode, wantExitCode)
			}
		}
	}
	if !spawned || !finished || errOut != wantErrOut || !strings.Contains(gotText.String(), wantText) {
		t.Fatalf("output spawned=%v finished=%v errOut=%v text=%q; want errOut=%v and text containing %q", spawned, finished, errOut, gotText.String(), wantErrOut, wantText)
	}
}

func TestHTTPChainsStayQueuedAndKeepTheirOperators(t *testing.T) {
	var requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.URL.Path == "/failure" {
			w.WriteHeader(http.StatusBadGateway)
			_, _ = io.WriteString(w, "fail")
			return
		}
		_, _ = io.WriteString(w, "ok")
	}))
	defer server.Close()

	for _, tc := range []struct {
		text        string
		wantPresent []string
		wantAbsent  string
	}{
		{text: "lookup ok ; echo second", wantPresent: []string{"ok", "second"}},
		{text: "lookup ok && echo second", wantPresent: []string{"ok", "second"}},
		{text: "lookup ok || echo second", wantPresent: []string{"ok"}, wantAbsent: "second"},
		{text: "lookup failure || echo fallback", wantPresent: []string{"fail", "fallback"}},
		{text: "lookup failure && echo skipped", wantPresent: []string{"fail"}, wantAbsent: "skipped"},
	} {
		t.Run(tc.text, func(t *testing.T) {
			requestCount := requests.Load()
			httpConfig := RunnerConfig{RawRunnerConfig: RawRunnerConfig{Runner: RunnerHTTP, Method: "GET", URL: server.URL + "/*"}}
			httpCommand := NewCommand(CommandConfig{
				Index:         1,
				MatcherConfig: MatcherConfig{RawMatcherConfig: RawMatcherConfig{Keyword: "lookup *"}},
				RunnerConfig:  httpConfig,
				ParserConfig:  ParserConfig{AllowInChain: true},
			}, NewHTTPRunner(httpConfig), nil)
			execCommand := NewCommand(CommandConfig{
				Index:         2,
				MatcherConfig: MatcherConfig{RawMatcherConfig: RawMatcherConfig{Keyword: "echo *"}},
				RunnerConfig:  RunnerConfig{RawRunnerConfig: RawRunnerConfig{Command: "/bin/echo *"}},
				ParserConfig:  ParserConfig{AllowInChain: true},
			}, NewExecRunner(), nil)
			replies := NewCommandSet([]*Command{httpCommand, execCommand})
			root := NewCommand(CommandConfig{Index: 0, MatcherConfig: MatcherConfig{RawMatcherConfig: RawMatcherConfig{Keyword: "agent"}}, RunnerConfig: RunnerConfig{RawRunnerConfig: RawRunnerConfig{Command: "agent"}}}, nil, replies)
			commands := NewCommandSet([]*Command{root})
			if commands.MatchSingle("agent", []int{0}) != root {
				t.Fatal("test root did not match resolver input")
			}
			if matched, _ := replies.MatchReply(tc.text, []int{1, 2}); matched != httpCommand {
				t.Fatalf("reply match = %p, want HTTP command %p", matched, httpCommand)
			}
			outputs := make(chan *CommandOutput, 30)
			executor := NewExecutor(commands, outputs)
			var queued *CommandInput
			dispatcher := NewCommandDispatcher(context.Background(), executor, &StdinStore{}, &ConversationLocks{}, func(input *CommandInput) bool {
				queued = input
				return true
			})
			router := NewConversationRouterWithRootInputResolver(commands, func(ConversationID) (RootCommandInput, error) {
				return RootCommandInput{Text: "agent", AllowedCommandIndexes: []int{0}}, nil
			}, dispatcher, 1)
			input := &CommandInput{Text: tc.text, ConversationID: ConversationID{ChannelID: "C", RootTimestamp: "1"}, MessageID: MessageID{Timestamp: "2"}, AllowedCommandIndexes: []int{1, 2}}
			if result, err := router.Accept(input); err != nil || result != AcceptRouted || queued != input {
				t.Fatalf("Accept() = %v, %v; queued=%p input=%p", result, err, queued, input)
			}
			if requests.Load() != requestCount {
				t.Fatal("HTTP chain started before worker execution")
			}
			executor.Execute(context.Background(), queued, nil)
			if got := requests.Load(); got != requestCount+1 {
				t.Fatalf("HTTP requests = %d, want %d", got-requestCount, 1)
			}
			dispatcher.Close()
			dispatcher.Wait()
			var body strings.Builder
			for len(outputs) > 0 {
				body.WriteString((<-outputs).Text)
			}
			for _, want := range tc.wantPresent {
				if !strings.Contains(body.String(), want) {
					t.Fatalf("output %q does not contain %q", body.String(), want)
				}
			}
			if tc.wantAbsent != "" && strings.Contains(body.String(), tc.wantAbsent) {
				t.Fatalf("output %q contains skipped command output %q", body.String(), tc.wantAbsent)
			}
		})
	}
}

func TestSameConversationHTTPRepliesRunSerially(t *testing.T) {
	started := make(chan string, 2)
	releaseFirst := make(chan struct{})
	releaseSecond := make(chan struct{})
	var releaseFirstOnce, releaseSecondOnce sync.Once
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started <- r.URL.Path
		if r.URL.Path == "/first" {
			<-releaseFirst
		} else {
			<-releaseSecond
		}
		_, _ = io.WriteString(w, r.URL.Path)
	}))
	t.Cleanup(server.Close)

	httpConfig := RunnerConfig{RawRunnerConfig: RawRunnerConfig{Runner: RunnerHTTP, Method: "GET", URL: server.URL + "/*"}}
	httpCommand := NewCommand(CommandConfig{Index: 1, MatcherConfig: MatcherConfig{RawMatcherConfig: RawMatcherConfig{Keyword: "lookup *"}}, RunnerConfig: httpConfig}, NewHTTPRunner(httpConfig), nil)
	root := NewCommand(CommandConfig{Index: 0, MatcherConfig: MatcherConfig{RawMatcherConfig: RawMatcherConfig{Keyword: "agent"}}, RunnerConfig: RunnerConfig{RawRunnerConfig: RawRunnerConfig{Command: "agent"}}}, nil, NewCommandSet([]*Command{httpCommand}))
	commands := NewCommandSet([]*Command{root})
	outputs := make(chan *CommandOutput, 30)
	queued := make(chan *CommandInput, 2)
	dispatcher := NewCommandDispatcher(context.Background(), NewExecutor(commands, outputs), &StdinStore{}, &ConversationLocks{}, func(input *CommandInput) bool {
		queued <- input
		return true
	})
	router := NewConversationRouterWithRootInputResolver(commands, nil, dispatcher, 1)
	t.Cleanup(func() {
		dispatcher.Close()
		releaseFirstOnce.Do(func() { close(releaseFirst) })
		releaseSecondOnce.Do(func() { close(releaseSecond) })
		dispatcher.Wait()
	})
	conversation := ConversationID{ChannelID: "C", RootTimestamp: "1"}
	acceptRoutedInput(t, router, &CommandInput{Text: "agent", ConversationID: conversation, MessageID: MessageID{Timestamp: "1"}, AllowedCommandIndexes: []int{0}})
	<-queued
	acceptRoutedInput(t, router, &CommandInput{Text: "lookup first", ConversationID: conversation, MessageID: MessageID{Timestamp: "2"}, AllowedCommandIndexes: []int{1}})
	waitForRequestPath(t, started, "/first")
	acceptRoutedInput(t, router, &CommandInput{Text: "lookup second", ConversationID: conversation, MessageID: MessageID{Timestamp: "3"}, AllowedCommandIndexes: []int{1}})
	assertNoHTTPRequestStarted(t, started)
	releaseFirstOnce.Do(func() { close(releaseFirst) })
	waitForRequestPath(t, started, "/second")
	releaseSecondOnce.Do(func() { close(releaseSecond) })
	dispatcher.Close()
	dispatcher.Wait()
}

func acceptRoutedInput(t *testing.T, router *ConversationRouter, input *CommandInput) {
	t.Helper()
	if result, err := router.Accept(input); err != nil || result != AcceptRouted {
		t.Fatalf("Accept(%q) = %v, %v; want routed", input.Text, result, err)
	}
}

func waitForRequestPath(t *testing.T, started <-chan string, want string) {
	t.Helper()
	select {
	case path := <-started:
		if path != want {
			t.Fatalf("request path = %q, want %q", path, want)
		}
	case <-time.After(3 * time.Second):
		t.Fatalf("request %q did not start", want)
	}
}

func assertNoHTTPRequestStarted(t *testing.T, started <-chan string) {
	t.Helper()
	select {
	case path := <-started:
		t.Fatalf("request %q started while the first was blocked", path)
	case <-time.After(100 * time.Millisecond):
	}
}

func TestDispatcherCloseRejectsNewWorkAndWaitsForOutputDrain(t *testing.T) {
	requestStarted := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		requestStarted <- struct{}{}
		<-r.Context().Done()
	}))
	t.Cleanup(server.Close)
	ctx, cancel := context.WithCancel(context.Background())
	outputs := make(chan *CommandOutput, 1)
	commands := NewCommandSet([]*Command{
		NewCommand(CommandConfig{Index: 0, MatcherConfig: MatcherConfig{RawMatcherConfig: RawMatcherConfig{Keyword: "lookup"}}, RunnerConfig: RunnerConfig{RawRunnerConfig: RawRunnerConfig{Runner: RunnerHTTP, Method: "GET", URL: server.URL}}}, NewHTTPRunner(RunnerConfig{RawRunnerConfig: RawRunnerConfig{Runner: RunnerHTTP, Method: "GET", URL: server.URL}}), nil),
		NewCommand(CommandConfig{Index: 1, MatcherConfig: MatcherConfig{RawMatcherConfig: RawMatcherConfig{Keyword: "echo"}}, RunnerConfig: RunnerConfig{RawRunnerConfig: RawRunnerConfig{Command: "/bin/echo"}}}, NewExecRunner(), nil),
	})
	queue := make(chan *CommandInput, 1)
	dispatcher := NewCommandDispatcher(ctx, NewExecutor(commands, outputs), &StdinStore{}, &ConversationLocks{}, func(input *CommandInput) bool {
		select {
		case queue <- input:
			return true
		default:
			return false
		}
	})
	writerDone := make(chan []*CommandOutput, 1)
	go func() {
		var written []*CommandOutput
		for output := range outputs {
			written = append(written, output)
		}
		writerDone <- written
		close(writerDone)
	}()
	var cleanupOnce sync.Once
	var queueCloseOnce, outputCloseOnce sync.Once
	t.Cleanup(func() {
		cleanupOnce.Do(func() {
			dispatcher.Close()
			cancel()
			queueCloseOnce.Do(func() { close(queue) })
			dispatcher.Wait()
			outputCloseOnce.Do(func() { close(outputs) })
			<-writerDone
		})
	})
	router := NewConversationRouterWithRootInputResolver(commands, nil, dispatcher, 1)
	if result, err := router.Accept(&CommandInput{Text: "lookup", ConversationID: ConversationID{ChannelID: "C", RootTimestamp: "1"}, MessageID: MessageID{Timestamp: "1"}, AllowedCommandIndexes: []int{0}}); err != nil || result != AcceptRouted {
		t.Fatalf("HTTP Accept() = %v, %v", result, err)
	}
	select {
	case <-requestStarted:
	case <-time.After(3 * time.Second):
		t.Fatal("HTTP request did not start")
	}
	dispatcher.Close()
	queueCloseOnce.Do(func() { close(queue) })
	if result, err := router.Accept(&CommandInput{Text: "echo", ConversationID: ConversationID{ChannelID: "D", RootTimestamp: "2"}, MessageID: MessageID{Timestamp: "2"}, AllowedCommandIndexes: []int{1}}); err != nil || result != AcceptIgnored {
		t.Fatalf("dispatch after Close() = %v, %v; want ignored", result, err)
	}
	waitDone := make(chan struct{})
	go func() { dispatcher.Wait(); close(waitDone) }()
	select {
	case <-waitDone:
		t.Fatal("Wait() returned before the HTTP command finished")
	case <-time.After(100 * time.Millisecond):
	}
	cancel()
	select {
	case <-waitDone:
	case <-time.After(3 * time.Second):
		t.Fatal("Wait() did not return after context cancellation")
	}
	outputCloseOnce.Do(func() { close(outputs) })
	written := <-writerDone
	assertCanceledCommandWasDrained(t, written)
}

func assertCanceledCommandWasDrained(t *testing.T, written []*CommandOutput) {
	t.Helper()
	finished := false
	for _, output := range written {
		finished = finished || output.Finished && output.ExitCode == 143
	}
	if !finished {
		t.Fatalf("output writer did not drain canceled command completion: %+v", written)
	}
}

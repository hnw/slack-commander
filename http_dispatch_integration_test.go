package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hnw/slack-commander/cmd"
)

func TestSingleHTTPDoesNotOccupyWorkerAndRunsAcrossConversations(t *testing.T) {
	started := make(chan string, 2)
	release := make(chan struct{})
	var releaseOnce sync.Once
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started <- r.URL.Path
		<-release
		_, _ = io.WriteString(w, r.URL.Path)
	}))
	t.Cleanup(server.Close)

	httpConfig := cmd.RunnerConfig{RawRunnerConfig: cmd.RawRunnerConfig{Runner: cmd.RunnerHTTP, Method: "GET", URL: server.URL + "/*"}}
	httpCommand := cmd.NewCommand(cmd.CommandConfig{
		Index:         0,
		MatcherConfig: cmd.MatcherConfig{RawMatcherConfig: cmd.RawMatcherConfig{Keyword: "http *"}},
		RunnerConfig:  httpConfig,
	}, cmd.NewHTTPRunner(httpConfig), nil)
	echoCommand := cmd.NewCommand(cmd.CommandConfig{
		Index:         1,
		MatcherConfig: cmd.MatcherConfig{RawMatcherConfig: cmd.RawMatcherConfig{Keyword: "echo *"}},
		RunnerConfig:  cmd.RunnerConfig{RawRunnerConfig: cmd.RawRunnerConfig{Command: "/bin/echo *"}},
	}, cmd.NewExecRunner(), nil)
	commands := cmd.NewCommandSet([]*cmd.Command{httpCommand, echoCommand})
	requests := make(chan *cmd.CommandInput, 10)
	outputs := make(chan *cmd.CommandOutput, 50)
	stdinStore := &cmd.StdinStore{}
	conversationLocks := &cmd.ConversationLocks{}
	ctx, cancel := context.WithCancel(context.Background())
	executor := cmd.NewExecutor(outputs)
	dispatcher := cmd.NewCommandDispatcher(ctx, executor, stdinStore, conversationLocks, func(input *cmd.CommandInput) bool {
		select {
		case requests <- input:
			return true
		default:
			return false
		}
	})
	router := cmd.NewConversationRouterWithRootInputResolver(commands, nil, dispatcher, 10)
	var workers sync.WaitGroup
	startWorkers(ctx, 1, requests, stdinStore, conversationLocks, executor, &workers)
	var queueClose sync.Once
	t.Cleanup(func() {
		dispatcher.Close()
		releaseOnce.Do(func() { close(release) })
		queueClose.Do(func() { close(requests) })
		cancel()
		workers.Wait()
		dispatcher.Wait()
	})

	for _, path := range []string{"/slow-a", "/slow-b"} {
		rootTimestamp := strings.TrimPrefix(path, "/")
		input := &cmd.CommandInput{
			Text:                  "http " + rootTimestamp,
			ConversationID:        cmd.ConversationID{ChannelID: "C", RootTimestamp: rootTimestamp},
			MessageID:             cmd.MessageID{Timestamp: rootTimestamp},
			AllowedCommandIndexes: []int{0},
		}
		acceptIntegrationInput(t, router, input)
	}
	waitForStartedIntegrationRequests(t, started, 2)

	queuedConversation := cmd.ConversationID{ChannelID: "C", RootTimestamp: "queued"}
	acceptIntegrationInput(t, router, &cmd.CommandInput{
		Text:                  "echo queued",
		ConversationID:        queuedConversation,
		MessageID:             cmd.MessageID{Timestamp: queuedConversation.RootTimestamp},
		AllowedCommandIndexes: []int{1},
	})
	collected := collectUntilQueuedCommandFinished(t, outputs, queuedConversation)

	releaseOnce.Do(func() { close(release) })
	dispatcher.Close()
	queueClose.Do(func() { close(requests) })
	workers.Wait()
	dispatcher.Wait()
	for len(outputs) > 0 {
		collected = append(collected, <-outputs)
	}

	assertIntegrationOutputs(t, collected, queuedConversation)
}

func acceptIntegrationInput(t *testing.T, router *cmd.ConversationRouter, input *cmd.CommandInput) {
	t.Helper()
	if result, err := router.Accept(input); err != nil || result != cmd.AcceptRouted {
		t.Fatalf("Accept(%q) = %v, %v; want routed", input.Text, result, err)
	}
}

func waitForStartedIntegrationRequests(t *testing.T, started <-chan string, count int) {
	t.Helper()
	for range count {
		select {
		case <-started:
		case <-time.After(3 * time.Second):
			t.Fatal("HTTP requests did not run concurrently")
		}
	}
}

func collectUntilQueuedCommandFinished(t *testing.T, outputs <-chan *cmd.CommandOutput, conversation cmd.ConversationID) []*cmd.CommandOutput {
	t.Helper()
	collected := make([]*cmd.CommandOutput, 0, 10)
	for {
		select {
		case output := <-outputs:
			collected = append(collected, output)
			if output.ConversationID == conversation && output.Finished {
				return collected
			}
		case <-time.After(3 * time.Second):
			t.Fatal("queued command waited for the HTTP requests to finish")
		}
	}
}

func assertIntegrationOutputs(t *testing.T, outputs []*cmd.CommandOutput, queuedConversation cmd.ConversationID) {
	t.Helper()
	seenHTTP := map[string]bool{}
	var queuedText string
	for _, output := range outputs {
		if output.ConversationID == queuedConversation {
			queuedText += output.Text
		}
		if output.ConversationID.ChannelID == "C" && output.ConversationID.RootTimestamp != queuedConversation.RootTimestamp && output.Text != "" {
			if output.IsErrOut || output.ExitCode != 0 {
				t.Fatalf("HTTP output = %+v", output)
			}
			seenHTTP[output.ConversationID.RootTimestamp] = strings.Contains(output.Text, "/slow-")
		}
	}
	if queuedText != "queued\n" {
		t.Fatalf("queued output = %q, want %q", queuedText, "queued\n")
	}
	for _, id := range []string{"slow-a", "slow-b"} {
		if !seenHTTP[id] {
			t.Fatalf("missing HTTP response for conversation %q: %v", id, seenHTTP)
		}
	}
}

func TestSameConversationQueuedAndHTTPCommandsSerialize(t *testing.T) {
	startedQueued := make(chan struct{}, 1)
	releaseQueued := make(chan struct{})
	serverStarted := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		serverStarted <- struct{}{}
		_, _ = io.WriteString(w, "http")
	}))
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stdinStore := &cmd.StdinStore{}
	locks := &cmd.ConversationLocks{}
	outputs := make(chan *cmd.CommandOutput, 30)
	httpConfig := cmd.RunnerConfig{RawRunnerConfig: cmd.RawRunnerConfig{Runner: cmd.RunnerHTTP, Method: "GET", URL: server.URL}}
	httpCommand := cmd.NewCommand(cmd.CommandConfig{Index: 1, MatcherConfig: cmd.MatcherConfig{RawMatcherConfig: cmd.RawMatcherConfig{Keyword: "http"}}, RunnerConfig: httpConfig}, cmd.NewHTTPRunner(httpConfig), nil)
	queuedCommand := cmd.NewCommand(cmd.CommandConfig{Index: 0, MatcherConfig: cmd.MatcherConfig{RawMatcherConfig: cmd.RawMatcherConfig{Keyword: "hold"}}, RunnerConfig: cmd.RunnerConfig{RawRunnerConfig: cmd.RawRunnerConfig{Command: "hold"}}}, blockedIntegrationRunner{started: startedQueued, release: releaseQueued}, cmd.NewCommandSet([]*cmd.Command{httpCommand}))
	commands := cmd.NewCommandSet([]*cmd.Command{queuedCommand})
	requests := make(chan *cmd.CommandInput, 10)
	executor := cmd.NewExecutor(outputs)
	dispatcher := cmd.NewCommandDispatcher(ctx, executor, stdinStore, locks, func(input *cmd.CommandInput) bool {
		select {
		case requests <- input:
			return true
		default:
			return false
		}
	})
	router := cmd.NewConversationRouterWithRootInputResolver(commands, nil, dispatcher, 10)
	var workers sync.WaitGroup
	startWorkers(ctx, 1, requests, stdinStore, locks, executor, &workers)
	var queueClose sync.Once
	var releaseClose sync.Once
	t.Cleanup(func() {
		dispatcher.Close()
		releaseClose.Do(func() { close(releaseQueued) })
		queueClose.Do(func() { close(requests) })
		cancel()
		workers.Wait()
		dispatcher.Wait()
	})
	conversation := cmd.ConversationID{ChannelID: "C", RootTimestamp: "1"}
	if result, err := router.Accept(&cmd.CommandInput{Text: "hold", ConversationID: conversation, MessageID: cmd.MessageID{Timestamp: "1"}, AllowedCommandIndexes: []int{0}}); err != nil || result != cmd.AcceptRouted {
		t.Fatalf("queued Accept() = %v, %v", result, err)
	}
	select {
	case <-startedQueued:
	case <-time.After(3 * time.Second):
		t.Fatal("queued command did not start")
	}
	if result, err := router.Accept(&cmd.CommandInput{Text: "http", ConversationID: conversation, MessageID: cmd.MessageID{Timestamp: "2"}, AllowedCommandIndexes: []int{1}}); err != nil || result != cmd.AcceptRouted {
		t.Fatalf("HTTP Accept() = %v, %v", result, err)
	}
	select {
	case <-serverStarted:
		t.Fatal("HTTP command ran before the queued command released the conversation")
	case <-time.After(100 * time.Millisecond):
	}
	releaseClose.Do(func() { close(releaseQueued) })
	select {
	case <-serverStarted:
	case <-time.After(3 * time.Second):
		t.Fatal("HTTP command did not run after the queued command released the conversation")
	}
	dispatcher.Close()
	queueClose.Do(func() { close(requests) })
	workers.Wait()
	dispatcher.Wait()
}

type blockedIntegrationRunner struct {
	started chan struct{}
	release <-chan struct{}
}

func (r blockedIntegrationRunner) CommandContext(context.Context, string, ...string) cmd.Cmd {
	return &blockedIntegrationCommand{started: r.started, release: r.release}
}

type blockedIntegrationCommand struct {
	started chan struct{}
	release <-chan struct{}
}

func (*blockedIntegrationCommand) SetStdin(io.Reader)  {}
func (*blockedIntegrationCommand) SetStdout(io.Writer) {}
func (*blockedIntegrationCommand) SetStderr(io.Writer) {}
func (c *blockedIntegrationCommand) Run(int) int {
	c.started <- struct{}{}
	<-c.release
	return 0
}

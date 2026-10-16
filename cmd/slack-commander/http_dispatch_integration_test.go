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

	"github.com/hnw/slack-commander/internal/command"
	"github.com/hnw/slack-commander/internal/messaging"
)

func TestHTTPOnlyChainsDoNotOccupyWorkerAndRunAcrossConversations(t *testing.T) {
	outputs := make(chan *observedCommandOutput, 50)

	started := make(chan string, 2)
	release := make(chan struct{})
	var releaseOnce sync.Once
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started <- r.URL.Path
		<-release
		_, _ = io.WriteString(w, r.URL.Path) //nolint:gosec // G705 -- test server echoes a controlled request path
	}))
	t.Cleanup(server.Close)

	httpConfig := command.RunnerConfig{RawRunnerConfig: command.RawRunnerConfig{Runner: command.RunnerHTTP, Method: "GET", URL: server.URL + "/*"}}
	httpCommand := command.NewCommand(command.CommandConfig{
		Index: 0, ParserConfig: command.ParserConfig{AllowInChain: true},
		Dispatch: command.DispatchExecutor,
	}, command.NewMatcher(command.MatcherConfig{RawMatcherConfig: command.RawMatcherConfig{Keyword: "http *"}}), command.NewHTTPRunner(httpConfig), nil, testOutputFactory(outputs)(messaging.ReplyConfig{}, 0))
	echoCommand := command.NewCommand(command.CommandConfig{
		Index: 1,
	}, command.NewMatcher(command.MatcherConfig{RawMatcherConfig: command.RawMatcherConfig{Keyword: "echo *"}}), command.NewExecRunner(command.RunnerConfig{RawRunnerConfig: command.RawRunnerConfig{Command: "/bin/echo *"}}), nil, testOutputFactory(outputs)(messaging.ReplyConfig{}, 0))
	commands := command.NewCommandSet([]*command.Command{httpCommand, echoCommand})
	requests := make(chan *command.CommandInput, 10)

	stdinStore := &command.StdinStore{}
	conversationLocks := &command.ConversationLocks{}
	ctx, cancel := context.WithCancel(context.Background())

	executor := command.NewExecutor()
	dispatcher := command.NewCommandDispatcher(ctx, executor, stdinStore, conversationLocks, func(input *command.CommandInput) bool {
		select {
		case requests <- input:
			return true
		default:
			return false
		}
	})
	router := command.NewConversationRouter(stdinStore, commands, nil, dispatcher, 10)
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
		input := &command.CommandInput{
			Text:                  "http " + rootTimestamp + " ; http " + rootTimestamp,
			ConversationID:        command.ConversationID{ChannelID: "C", RootTimestamp: rootTimestamp},
			MessageID:             command.MessageID{Timestamp: rootTimestamp},
			AllowedCommandIndexes: []int{0},
		}
		acceptIntegrationInput(t, router, input)
	}
	waitForStartedIntegrationRequests(t, started, 2)

	queuedConversation := command.ConversationID{ChannelID: "C", RootTimestamp: "queued"}
	acceptIntegrationInput(t, router, &command.CommandInput{
		Text:                  "echo queued",
		ConversationID:        queuedConversation,
		MessageID:             command.MessageID{Timestamp: queuedConversation.RootTimestamp},
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

func TestHTTPOnlyChainAndQueuedCommandShareConversationLock(t *testing.T) {
	outputs := make(chan *observedCommandOutput, 20)

	started := make(chan string, 2)
	releaseFirst, releaseSecond := make(chan struct{}), make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started <- r.URL.Path
		if r.URL.Path == "/one" {
			<-releaseFirst
		} else {
			<-releaseSecond
		}
		_, _ = io.WriteString(w, "ok")
	}))
	t.Cleanup(server.Close)
	httpConfig := command.RunnerConfig{RawRunnerConfig: command.RawRunnerConfig{Runner: command.RunnerHTTP, Method: "GET", URL: server.URL + "/*"}}
	httpCommand := command.NewCommand(command.CommandConfig{
		Index: 0, ParserConfig: command.ParserConfig{AllowInChain: true}, Dispatch: command.DispatchExecutor,
	}, command.NewMatcher(command.MatcherConfig{RawMatcherConfig: command.RawMatcherConfig{Keyword: "http *"}}), command.NewHTTPRunner(httpConfig), nil, testOutputFactory(outputs)(messaging.ReplyConfig{}, 0))
	queuedStarted, queuedRelease := make(chan struct{}, 1), make(chan struct{})
	queuedCommand := command.NewCommand(command.CommandConfig{
		Index: 1,
	}, command.NewMatcher(command.MatcherConfig{RawMatcherConfig: command.RawMatcherConfig{Keyword: "hold"}}), blockedIntegrationRunner{started: queuedStarted, release: queuedRelease}, nil, testOutputFactory(outputs)(messaging.ReplyConfig{}, 0))
	commands := command.NewCommandSet([]*command.Command{httpCommand, queuedCommand})
	conversation := command.ConversationID{ChannelID: "C", RootTimestamp: "1"}
	requests := make(chan *command.CommandInput, 2)
	stdinStore, locks := &command.StdinStore{}, &command.ConversationLocks{}
	ctx, cancel := context.WithCancel(context.Background())

	executor := command.NewExecutor()
	dispatcher := command.NewCommandDispatcher(ctx, executor, stdinStore, locks, func(input *command.CommandInput) bool {
		requests <- input
		return true
	})
	var workers sync.WaitGroup
	startWorkers(ctx, 1, requests, stdinStore, locks, executor, &workers)
	var firstOnce, secondOnce, queuedOnce, requestCloseOnce sync.Once
	defer func() {
		dispatcher.Close()
		firstOnce.Do(func() { close(releaseFirst) })
		secondOnce.Do(func() { close(releaseSecond) })
		queuedOnce.Do(func() { close(queuedRelease) })
		requestCloseOnce.Do(func() { close(requests) })
		cancel()
		workers.Wait()
		dispatcher.Wait()
	}()
	chainText := "http one ; http two"
	chainInput := &command.CommandInput{Text: chainText, ConversationID: conversation}
	chainInput.Commands, chainInput.ParseErr = commands.Resolve(chainText, []int{0})
	if chainInput.ParseErr != nil {
		t.Fatal(chainInput.ParseErr)
	}
	if got := dispatcher.Dispatch(chainInput); got != command.DispatchAccepted {
		t.Fatalf("HTTP chain Dispatch() = %v", got)
	}
	waitForIntegrationPath(t, started, "/one")
	queuedInput := &command.CommandInput{Text: "hold", ConversationID: conversation}
	queuedInput.Commands, queuedInput.ParseErr = commands.Resolve("hold", []int{1})
	if queuedInput.ParseErr != nil {
		t.Fatal(queuedInput.ParseErr)
	}
	if got := dispatcher.Dispatch(queuedInput); got != command.DispatchAccepted {
		t.Fatalf("queued command Dispatch() = %v", got)
	}
	assertIntegrationSignalBlocked(t, queuedStarted, "queued command passed the conversation lock during the HTTP chain")
	firstOnce.Do(func() { close(releaseFirst) })
	waitForIntegrationPath(t, started, "/two")
	assertIntegrationSignalBlocked(t, queuedStarted, "queued command passed the conversation lock before the HTTP chain finished")
	secondOnce.Do(func() { close(releaseSecond) })
	waitForIntegrationSignal(t, queuedStarted, "queued command did not start after the HTTP chain finished")
	queuedOnce.Do(func() { close(queuedRelease) })
	dispatcher.Close()
	requestCloseOnce.Do(func() { close(requests) })
	workers.Wait()
	dispatcher.Wait()
}

func waitForIntegrationPath(t *testing.T, started <-chan string, want string) {
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

func waitForIntegrationSignal(t *testing.T, started <-chan struct{}, message string) {
	t.Helper()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal(message)
	}
}

func assertIntegrationSignalBlocked(t *testing.T, started <-chan struct{}, message string) {
	t.Helper()
	select {
	case <-started:
		t.Fatal(message)
	case <-time.After(100 * time.Millisecond):
	}
}

func acceptIntegrationInput(t *testing.T, router *command.ConversationRouter, input *command.CommandInput) {
	t.Helper()
	if result, err := router.Accept(context.Background(), input); err != nil || result != command.AcceptRouted {
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

func collectUntilQueuedCommandFinished(t *testing.T, outputs <-chan *observedCommandOutput, conversation command.ConversationID) []*observedCommandOutput {
	t.Helper()
	collected := make([]*observedCommandOutput, 0, 10)
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

func assertIntegrationOutputs(t *testing.T, outputs []*observedCommandOutput, queuedConversation command.ConversationID) {
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
	outputs := make(chan *observedCommandOutput, 30)

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
	stdinStore := &command.StdinStore{}
	locks := &command.ConversationLocks{}

	httpConfig := command.RunnerConfig{RawRunnerConfig: command.RawRunnerConfig{Runner: command.RunnerHTTP, Method: "GET", URL: server.URL}}
	httpCommand := command.NewCommand(command.CommandConfig{Index: 1, Dispatch: command.DispatchExecutor}, command.NewMatcher(command.MatcherConfig{RawMatcherConfig: command.RawMatcherConfig{Keyword: "http"}}), command.NewHTTPRunner(httpConfig), nil, testOutputFactory(outputs)(messaging.ReplyConfig{}, 0))
	queuedCommand := command.NewCommand(command.CommandConfig{Index: 0}, command.NewMatcher(command.MatcherConfig{RawMatcherConfig: command.RawMatcherConfig{Keyword: "hold"}}), blockedIntegrationRunner{started: startedQueued, release: releaseQueued}, command.NewCommandSet([]*command.Command{httpCommand}), testOutputFactory(outputs)(messaging.ReplyConfig{}, 0))
	commands := command.NewCommandSet([]*command.Command{queuedCommand})
	requests := make(chan *command.CommandInput, 10)

	executor := command.NewExecutor()
	dispatcher := command.NewCommandDispatcher(ctx, executor, stdinStore, locks, func(input *command.CommandInput) bool {
		select {
		case requests <- input:
			return true
		default:
			return false
		}
	})
	router := command.NewConversationRouter(stdinStore, commands, nil, dispatcher, 10)
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
	conversation := command.ConversationID{ChannelID: "C", RootTimestamp: "1"}
	if result, err := router.Accept(context.Background(), &command.CommandInput{Text: "hold", ConversationID: conversation, MessageID: command.MessageID{Timestamp: "1"}, AllowedCommandIndexes: []int{0}}); err != nil || result != command.AcceptRouted {
		t.Fatalf("queued Accept() = %v, %v", result, err)
	}
	select {
	case <-startedQueued:
	case <-time.After(3 * time.Second):
		t.Fatal("queued command did not start")
	}
	if result, err := router.Accept(context.Background(), &command.CommandInput{Text: "http", ConversationID: conversation, MessageID: command.MessageID{Timestamp: "2"}, AllowedCommandIndexes: []int{1}}); err != nil || result != command.AcceptRouted {
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

func (r blockedIntegrationRunner) CommandContext(context.Context, []string, bool) command.Cmd {
	return &blockedIntegrationCommand{started: r.started, release: r.release}
}

type blockedIntegrationCommand struct {
	started chan struct{}
	release <-chan struct{}
}

func (*blockedIntegrationCommand) SetStdin(io.Reader)  {}
func (*blockedIntegrationCommand) SetStdout(io.Writer) {}
func (*blockedIntegrationCommand) SetStderr(io.Writer) {}
func (c *blockedIntegrationCommand) Run() int {
	c.started <- struct{}{}
	<-c.release
	return 0
}

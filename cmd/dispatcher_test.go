package cmd

import (
	"context"
	"errors"
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
				Dispatch:      DispatchExecutor,
				ExecutorConfig: ExecutorConfig{
					Timeout: tc.timeout,
				},
			}, NewHTTPRunner(config), nil)
			commands := NewCommandSet([]*Command{command})
			dispatcher := NewCommandDispatcher(ctx, NewExecutor(outputs), outputs, &StdinStore{}, &ConversationLocks{}, nil)
			router := NewConversationRouter(&StdinStore{}, commands, nil, dispatcher, 1)
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

func TestMixedChainsUseQueueAndKeepOperators(t *testing.T) {
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
				ParserConfig:  ParserConfig{AllowInChain: true}, Dispatch: DispatchExecutor,
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
			if commands.ResolveInput("agent", []int{0}).Commands[0].Command != root {
				t.Fatal("test root did not match resolver input")
			}
			parsed := replies.ResolveInput(tc.text, []int{1, 2})
			if parsed.Commands[0].Command != httpCommand {
				t.Fatalf("reply match = %p, want HTTP command %p", parsed.Commands[0].Command, httpCommand)
			}
			outputs := make(chan *CommandOutput, 30)
			executor := NewExecutor(outputs)
			var queued *CommandInput
			dispatcher := NewCommandDispatcher(context.Background(), executor, outputs, &StdinStore{}, &ConversationLocks{}, func(input *CommandInput) bool {
				queued = input
				return true
			})
			router := NewConversationRouter(&StdinStore{}, commands, func(ConversationID) (RootCommandInput, error) {
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

func TestMixedRootChainStaysQueuedAndCachesNoReplyCommand(t *testing.T) {
	httpConfig := RunnerConfig{RawRunnerConfig: RawRunnerConfig{Runner: RunnerHTTP, Method: "GET", URL: "http://127.0.0.1:1"}}
	httpCommand := NewCommand(CommandConfig{
		Index: 0, MatcherConfig: MatcherConfig{RawMatcherConfig: RawMatcherConfig{Keyword: "lookup"}},
		RunnerConfig: httpConfig, ParserConfig: ParserConfig{AllowInChain: true}, Dispatch: DispatchExecutor,
	}, NewHTTPRunner(httpConfig), nil)
	second := NewCommand(CommandConfig{
		Index: 1, MatcherConfig: MatcherConfig{RawMatcherConfig: RawMatcherConfig{Keyword: "echo"}},
		RunnerConfig: RunnerConfig{RawRunnerConfig: RawRunnerConfig{Command: "/bin/echo"}}, ParserConfig: ParserConfig{AllowInChain: true},
	}, NewExecRunner(), nil)
	commands := NewCommandSet([]*Command{httpCommand, second})
	var queued *CommandInput
	dispatcher := newTestCommandDispatcher(context.Background(), 10, func(input *CommandInput) bool {
		queued = input
		return true
	})
	router := NewConversationRouter(&StdinStore{}, commands, nil, dispatcher, 1)
	defer func() { dispatcher.Close(); dispatcher.Wait() }()
	conversation := ConversationID{ChannelID: "C", RootTimestamp: "1"}
	input := &CommandInput{
		Text: "lookup && echo", ConversationID: conversation, MessageID: MessageID{Timestamp: "1"},
		AllowedCommandIndexes: []int{0, 1},
	}
	if result, err := router.Accept(input); err != nil || result != AcceptRouted || queued != input {
		t.Fatalf("HTTP chain Accept() = %v, %v, queued=%p; want queued input", result, err, queued)
	}
	if got := input.ResolvedInput; got == nil || len(got.Commands) != 2 || got.Commands[0].Command != httpCommand {
		t.Fatalf("ResolvedInput = %#v, want two commands with HTTP first match", got)
	}
	if got, ok := router.routes.lookup(conversation); !ok || got != nil {
		t.Fatalf("cached reply command = %v, %v; want negative cache for chain", got, ok)
	}
	reply := &CommandInput{Text: "reply", ConversationID: conversation, MessageID: MessageID{Timestamp: "2"}, AllowedCommandIndexes: []int{0}}
	if result, err := router.Accept(reply); err != nil || result != AcceptIgnored {
		t.Fatalf("reply to oneshot chain = %v, %v; want ignored", result, err)
	}
}

func TestRootChainWithTrailingUnknownCachesNoReplyCommand(t *testing.T) {
	foo := NewCommand(CommandConfig{
		Index: 0, MatcherConfig: MatcherConfig{RawMatcherConfig: RawMatcherConfig{Keyword: "foo"}},
		RunnerConfig: RunnerConfig{RawRunnerConfig: RawRunnerConfig{Command: "foo"}},
		ParserConfig: ParserConfig{AllowInChain: true},
	}, nil, nil)
	commands := NewCommandSet([]*Command{foo})
	dispatcher := newTestCommandDispatcher(context.Background(), 10, func(*CommandInput) bool { return true })
	router := NewConversationRouter(&StdinStore{}, commands, nil, dispatcher, 1)
	conversation := ConversationID{ChannelID: "C", RootTimestamp: "1"}
	input := &CommandInput{Text: "foo ; unknown", ConversationID: conversation, MessageID: MessageID{Timestamp: "1"}, AllowedCommandIndexes: []int{0}}
	if result, err := router.Accept(input); err != nil || result != AcceptRouted {
		t.Fatalf("Accept() = %v, %v; want routed", result, err)
	}
	if got, ok := router.routes.lookup(conversation); !ok || got != nil {
		t.Fatalf("cached reply command = %v, %v; want negative cache for chain", got, ok)
	}
}

func TestDispatcherQueuesChainsAndExecutorKeepsOperators(t *testing.T) {
	for _, tc := range []struct {
		operator string
		want     []string
	}{
		{operator: ";", want: []string{"direct", "echo"}},
		{operator: "&&", want: []string{"direct", "echo"}},
		{operator: "||", want: []string{"direct"}},
	} {
		t.Run(tc.operator, func(t *testing.T) {
			runner := &fakeRunner{}
			first := NewCommand(CommandConfig{
				Index: 1, MatcherConfig: MatcherConfig{RawMatcherConfig: RawMatcherConfig{Keyword: "run *"}},
				RunnerConfig: RunnerConfig{RawRunnerConfig: RawRunnerConfig{Command: "direct *"}},
				ParserConfig: ParserConfig{AllowInChain: true},
			}, runner, nil)
			second := NewCommand(CommandConfig{
				Index: 2, MatcherConfig: MatcherConfig{RawMatcherConfig: RawMatcherConfig{Keyword: "echo *"}},
				RunnerConfig: RunnerConfig{RawRunnerConfig: RawRunnerConfig{Command: "echo *"}},
				ParserConfig: ParserConfig{AllowInChain: true},
			}, runner, nil)
			set := NewCommandSet([]*Command{first, second})
			text := "run original " + tc.operator + " echo later"
			input := &CommandInput{Text: text, ResolvedInput: set.ResolveInput(text, []int{1, 2}), AllowedCommandIndexes: []int{1, 2}}
			var queued *CommandInput
			dispatcher := newTestCommandDispatcher(context.Background(), 20, func(got *CommandInput) bool {
				queued = got
				return true
			})
			if got := dispatcher.Dispatch(input); got != DispatchAccepted || queued != input {
				t.Fatalf("Dispatch() = %v; queued=%p, want accepted queued input", got, queued)
			}
			if calls := runner.Calls(); len(calls) != 0 {
				t.Fatalf("Dispatch() ran direct command for chain: %#v", calls)
			}
			dispatcher.executor.Execute(context.Background(), queued, nil)
			calls := runner.Calls()
			if len(calls) != len(tc.want) {
				t.Fatalf("executor calls = %#v, want %v", calls, tc.want)
			}
			for i, name := range tc.want {
				if calls[i].name != name {
					t.Fatalf("executor calls = %#v, want %v", calls, tc.want)
				}
			}
		})
	}
}

func TestQueuedExecChainKeepsCommandNotFoundExitCode(t *testing.T) {
	runner := &fakeRunner{}
	exec := NewCommand(CommandConfig{
		MatcherConfig: MatcherConfig{RawMatcherConfig: RawMatcherConfig{Keyword: "echo *"}},
		RunnerConfig:  RunnerConfig{RawRunnerConfig: RawRunnerConfig{Command: "echo *"}},
		ParserConfig:  ParserConfig{AllowInChain: true},
	}, runner, nil)
	commands := NewCommandSet([]*Command{exec})
	input := &CommandInput{Text: "echo first ; unknown", ResolvedInput: commands.ResolveInput("echo first ; unknown", []int{0})}
	outputs := make(chan *CommandOutput, 10)
	var queued *CommandInput
	dispatcher := NewCommandDispatcher(context.Background(), NewExecutor(outputs), outputs, &StdinStore{}, &ConversationLocks{}, func(got *CommandInput) bool {
		queued = got
		return true
	})
	if got := dispatcher.Dispatch(input); got != DispatchAccepted || queued != input {
		t.Fatalf("Dispatch()=%v queued=%p, want queue acceptance", got, queued)
	}
	dispatcher.executor.Execute(context.Background(), queued, nil)
	if calls := runner.Calls(); len(calls) != 1 {
		t.Fatalf("runner calls = %#v, want first exec command", calls)
	}
	assertExecutorChainOutput(t, outputs, 127, []string{"コマンドが見つかりませんでした"}, "")
}

func TestQueuePolicyWinsForMixedChains(t *testing.T) {
	http := NewCommand(CommandConfig{
		Index: 1, MatcherConfig: MatcherConfig{RawMatcherConfig: RawMatcherConfig{Keyword: "http"}},
		RunnerConfig: RunnerConfig{RawRunnerConfig: RawRunnerConfig{Runner: RunnerHTTP, Method: "GET", URL: "https://example.com"}},
		ParserConfig: ParserConfig{AllowInChain: true}, Dispatch: DispatchExecutor,
	}, &fakeRunner{}, nil)
	exec := NewCommand(CommandConfig{
		Index: 2, MatcherConfig: MatcherConfig{RawMatcherConfig: RawMatcherConfig{Keyword: "exec"}},
		RunnerConfig: RunnerConfig{RawRunnerConfig: RawRunnerConfig{Command: "exec"}},
		ParserConfig: ParserConfig{AllowInChain: true},
	}, &fakeRunner{}, nil)
	compose := NewCommand(CommandConfig{
		Index: 3, MatcherConfig: MatcherConfig{RawMatcherConfig: RawMatcherConfig{Keyword: "compose"}},
		RunnerConfig: RunnerConfig{RawRunnerConfig: RawRunnerConfig{Runner: RunnerCompose, Command: "compose"}},
		ParserConfig: ParserConfig{AllowInChain: true},
	}, &fakeRunner{}, nil)
	set := NewCommandSet([]*Command{http, exec, compose})
	for _, text := range []string{"http ; exec", "exec ; http", "exec ; compose"} {
		t.Run(text, func(t *testing.T) {
			input := &CommandInput{Text: text, ResolvedInput: set.ResolveInput(text, []int{1, 2, 3})}
			var queued *CommandInput
			dispatcher := newTestCommandDispatcher(context.Background(), 10, func(got *CommandInput) bool {
				queued = got
				return true
			})
			if got := dispatcher.Dispatch(input); got != DispatchAccepted || queued != input {
				t.Fatalf("Dispatch() = %v; queued=%p, want mixed input queued", got, queued)
			}
		})
	}
}

func TestDispatcherSendsParseErrorBeforePolicyDispatch(t *testing.T) {
	runner := &fakeRunner{}
	systemReply := &struct{ broadcast bool }{broadcast: true}
	command := NewCommand(CommandConfig{
		Index: 1, MatcherConfig: MatcherConfig{RawMatcherConfig: RawMatcherConfig{Keyword: "run *"}},
		RunnerConfig: RunnerConfig{RawRunnerConfig: RawRunnerConfig{Command: "direct *"}},
		Dispatch:     DispatchRunner, SystemReplyConfig: systemReply,
	}, runner, nil)
	outputs := make(chan *CommandOutput, 1)
	queueCalls := 0
	dispatcher := NewCommandDispatcher(context.Background(), nil, outputs, &StdinStore{}, &ConversationLocks{}, func(*CommandInput) bool {
		queueCalls++
		return true
	})
	input := &CommandInput{Text: "malformed", ConversationID: ConversationID{ChannelID: "C", RootTimestamp: "1"}, MessageID: MessageID{ChannelID: "C", Timestamp: "2"}, ResolvedInput: &ResolvedInput{Commands: []ResolvedCommand{{Command: command, Args: []string{"run"}}}, ParseErr: errors.New("prepared parse error")}}
	if got := dispatcher.Dispatch(input); got != DispatchAccepted {
		t.Fatalf("Dispatch() = %v, want parse error accepted", got)
	}
	dispatcher.Wait()
	if calls := runner.Calls(); len(calls) != 0 {
		t.Fatalf("Runner ran malformed input: %#v", calls)
	}
	if queueCalls != 0 {
		t.Fatalf("queue calls = %d, want zero", queueCalls)
	}
	output := <-outputs
	if output.Text != "prepared parse error" || !output.IsErrOut || output.ExitCode != 2 || output.ReplyConfig != systemReply || output.ConversationID != input.ConversationID || output.MessageID != input.MessageID || output.Spawned || output.Finished {
		t.Fatalf("parse error output = %#v", output)
	}
}

func TestDispatcherWaitIncludesBlockedParseErrorOutput(t *testing.T) {
	outputs := make(chan *CommandOutput)
	command := NewCommand(CommandConfig{Dispatch: DispatchQueue}, nil, nil)
	dispatcher := NewCommandDispatcher(context.Background(), nil, outputs, &StdinStore{}, &ConversationLocks{}, func(*CommandInput) bool {
		t.Fatal("parse error was queued")
		return false
	})
	input := &CommandInput{ResolvedInput: &ResolvedInput{Commands: []ResolvedCommand{{Command: command}}, ParseErr: errors.New("blocked")}}
	if got := dispatcher.Dispatch(input); got != DispatchAccepted {
		t.Fatalf("Dispatch() = %v, want accepted", got)
	}
	dispatcher.Close()
	done := make(chan struct{})
	go func() { dispatcher.Wait(); close(done) }()
	select {
	case <-done:
		t.Fatal("Wait returned before output send completed")
	case <-time.After(20 * time.Millisecond):
	}
	if got := dispatcher.Dispatch(input); got != DispatchIgnored {
		t.Fatalf("Dispatch after Close() = %v, want ignored", got)
	}
	if output := <-outputs; output.Text != "blocked" {
		t.Fatalf("output = %#v", output)
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Wait did not finish after output send")
	}
}

func TestDispatcherIgnoresInputWithoutResolvedInput(t *testing.T) {
	queued := false
	dispatcher := newTestCommandDispatcher(context.Background(), 10, func(*CommandInput) bool {
		queued = true
		return true
	})
	if got := dispatcher.Dispatch(&CommandInput{Text: "run"}); got != DispatchIgnored || queued {
		t.Fatalf("Dispatch() = %v; queued=%v, want ignored without queue", got, queued)
	}
}

func TestDispatcherIgnoresInputsWithoutMatchedCommands(t *testing.T) {
	command := NewCommand(CommandConfig{
		MatcherConfig: MatcherConfig{RawMatcherConfig: RawMatcherConfig{Keyword: "known"}},
	}, nil, nil)
	commands := NewCommandSet([]*Command{command})
	queueCalls := 0
	dispatcher := NewCommandDispatcher(context.Background(), nil, make(chan *CommandOutput, 1), &StdinStore{}, &ConversationLocks{}, func(*CommandInput) bool {
		queueCalls++
		return true
	})
	for _, text := range []string{"", "unknown", "unknown ; unknown"} {
		input := &CommandInput{Text: text, ResolvedInput: commands.ResolveInput(text, []int{0})}
		if got := dispatcher.Dispatch(input); got != DispatchIgnored {
			t.Fatalf("Dispatch(%q) = %v, want ignored", text, got)
		}
	}
	if queueCalls != 0 {
		t.Fatalf("queue calls = %d, want zero", queueCalls)
	}
}

func TestDispatcherIgnoresChainsWhoseFirstCommandIsUnmatched(t *testing.T) {
	httpConfig := RunnerConfig{RawRunnerConfig: RawRunnerConfig{Runner: RunnerHTTP, Method: "GET", URL: "http://127.0.0.1:1"}}
	http := NewCommand(CommandConfig{
		Index: 1, MatcherConfig: MatcherConfig{RawMatcherConfig: RawMatcherConfig{Keyword: "http"}},
		RunnerConfig: httpConfig, ParserConfig: ParserConfig{AllowInChain: true}, Dispatch: DispatchExecutor,
	}, NewHTTPRunner(httpConfig), nil)
	queue := NewCommand(CommandConfig{
		Index: 2, MatcherConfig: MatcherConfig{RawMatcherConfig: RawMatcherConfig{Keyword: "exec"}},
		ParserConfig: ParserConfig{AllowInChain: true}, Dispatch: DispatchQueue,
	}, nil, nil)
	commands := NewCommandSet([]*Command{http, queue})
	queueCalls := 0
	outputs := make(chan *CommandOutput, 10)
	dispatcher := NewCommandDispatcher(context.Background(), NewExecutor(outputs), outputs, &StdinStore{}, &ConversationLocks{}, func(*CommandInput) bool {
		queueCalls++
		return true
	})
	for _, text := range []string{
		"unknown ; http", "unknown && http", "unknown || http",
		"unknown ; exec", "unknown && exec", "unknown || exec",
	} {
		input := &CommandInput{Text: text, ResolvedInput: commands.ResolveInput(text, []int{1, 2})}
		if got := dispatcher.Dispatch(input); got != DispatchIgnored {
			t.Fatalf("Dispatch(%q) = %v, want ignored", text, got)
		}
	}
	dispatcher.Close()
	dispatcher.Wait()
	if queueCalls != 0 || len(outputs) != 0 {
		t.Fatalf("queue calls = %d, outputs = %d; want no dispatch", queueCalls, len(outputs))
	}
}

func TestDispatcherEnforcesAllowInChainBeforeDispatch(t *testing.T) {
	first := NewCommand(CommandConfig{
		Index: 1, MatcherConfig: MatcherConfig{RawMatcherConfig: RawMatcherConfig{Keyword: "first"}},
		RunnerConfig: RunnerConfig{RawRunnerConfig: RawRunnerConfig{Command: "first"}},
		ParserConfig: ParserConfig{AllowInChain: true},
	}, nil, nil)
	blocked := NewCommand(CommandConfig{
		Index: 2, MatcherConfig: MatcherConfig{RawMatcherConfig: RawMatcherConfig{Keyword: "blocked"}},
		RunnerConfig: RunnerConfig{RawRunnerConfig: RawRunnerConfig{Command: "blocked"}},
		ParserConfig: ParserConfig{AllowInChain: false},
	}, nil, nil)
	set := NewCommandSet([]*Command{first, blocked})
	for _, tc := range []struct {
		text string
		want DispatchResult
	}{
		{text: "first ; blocked", want: DispatchIgnored},
		{text: "first && blocked", want: DispatchIgnored},
		{text: "first || blocked", want: DispatchIgnored},
		{text: "blocked ; first", want: DispatchIgnored},
		{text: "blocked", want: DispatchAccepted},
		{text: "first ; first", want: DispatchAccepted},
		{text: "first ; unknown", want: DispatchAccepted},
	} {
		t.Run(tc.text, func(t *testing.T) {
			queued := false
			dispatcher := newTestCommandDispatcher(context.Background(), 10, func(*CommandInput) bool {
				queued = true
				return true
			})
			input := &CommandInput{Text: tc.text, ResolvedInput: set.ResolveInput(tc.text, []int{1, 2})}
			if got := dispatcher.Dispatch(input); got != tc.want {
				target, targetOK := input.ResolvedInput.DispatchTarget()
				t.Fatalf("Dispatch() = %v, want %v; resolved=%#v target=(%v,%v)", got, tc.want, input.ResolvedInput, target, targetOK)
			}
			if queued != (tc.want == DispatchAccepted) {
				t.Fatalf("queued = %v, want accepted=%v", queued, tc.want == DispatchAccepted)
			}
		})
	}
}

func TestDispatcherReportsParseErrorBeforeChainPolicy(t *testing.T) {
	command := NewCommand(CommandConfig{
		Index: 1, MatcherConfig: MatcherConfig{RawMatcherConfig: RawMatcherConfig{Keyword: "first"}},
		ParserConfig: ParserConfig{AllowInChain: false},
	}, nil, nil)
	outputs := make(chan *CommandOutput, 1)
	dispatcher := NewCommandDispatcher(context.Background(), nil, outputs, &StdinStore{}, &ConversationLocks{}, nil)
	input := &CommandInput{ResolvedInput: &ResolvedInput{
		Commands: []ResolvedCommand{{Command: command}, {Command: command}},
		ParseErr: errors.New("parse failure"),
	}}
	if got := dispatcher.Dispatch(input); got != DispatchAccepted {
		t.Fatalf("Dispatch() = %v, want parse error accepted", got)
	}
	dispatcher.Close()
	dispatcher.Wait()
	if output := <-outputs; output.Text != "parse failure" {
		t.Fatalf("parse error output = %q", output.Text)
	}
}

func TestDispatcherKeepsParseErrorSilentWhenFirstPartIsUnmatched(t *testing.T) {
	command := NewCommand(CommandConfig{Dispatch: DispatchQueue}, nil, nil)
	outputs := make(chan *CommandOutput, 1)
	queueCalls := 0
	dispatcher := NewCommandDispatcher(context.Background(), nil, outputs, &StdinStore{}, &ConversationLocks{}, func(*CommandInput) bool {
		queueCalls++
		return true
	})
	input := &CommandInput{ResolvedInput: &ResolvedInput{
		Commands: []ResolvedCommand{{}, {Command: command}},
		ParseErr: errors.New("malformed"),
	}}
	if got := dispatcher.Dispatch(input); got != DispatchIgnored || queueCalls != 0 || len(outputs) != 0 {
		t.Fatalf("Dispatch()=%v queueCalls=%d outputs=%d; want silent ignored input", got, queueCalls, len(outputs))
	}
}

func TestDispatcherRejectsRunnerChain(t *testing.T) {
	runner := NewCommand(CommandConfig{Dispatch: DispatchRunner}, nil, nil)
	queued := false
	dispatcher := newTestCommandDispatcher(context.Background(), 10, func(*CommandInput) bool {
		queued = true
		return true
	})
	for _, commands := range [][]ResolvedCommand{
		{{Command: runner}, {}},
		{{}, {Command: runner}},
	} {
		input := &CommandInput{ResolvedInput: &ResolvedInput{Commands: commands}}
		if got := dispatcher.Dispatch(input); got != DispatchIgnored || queued {
			t.Fatalf("Dispatch() = %v; queued=%v, want invalid runner chain ignored", got, queued)
		}
	}
}

func TestDispatcherRunnerBypassesConversationLockAndExecutor(t *testing.T) {
	conversation := ConversationID{ChannelID: "C", RootTimestamp: "1"}
	locks := &ConversationLocks{}
	unlock := locks.Lock(conversation)
	runner := &fakeRunner{}
	command := NewCommand(CommandConfig{Dispatch: DispatchRunner}, runner, nil)
	outputs := make(chan *CommandOutput, 10)
	queued := false
	dispatcher := NewCommandDispatcher(context.Background(), NewExecutor(outputs), outputs, &StdinStore{}, locks, func(*CommandInput) bool {
		queued = true
		return true
	})
	result := make(chan DispatchResult, 1)
	go func() {
		result <- dispatcher.Dispatch(&CommandInput{
			ConversationID: conversation,
			ResolvedInput:  &ResolvedInput{Commands: []ResolvedCommand{{Command: command, Args: []string{"reply"}}}},
		})
	}()
	select {
	case got := <-result:
		unlock()
		if got != DispatchAccepted || queued || len(runner.Calls()) != 1 || len(outputs) != 0 {
			t.Fatalf("Dispatch() = %v; queued=%v runner calls=%d outputs=%d", got, queued, len(runner.Calls()), len(outputs))
		}
	case <-time.After(time.Second):
		unlock()
		t.Fatal("DispatchRunner waited on ConversationLock")
	}
}

func testHTTPCommand(index int, keyword string, inputBodyMode InputBodyMode) *Command {
	config := RunnerConfig{RawRunnerConfig: RawRunnerConfig{Runner: RunnerHTTP, Method: "GET", URL: "http://127.0.0.1:1"}}
	return NewCommand(CommandConfig{
		Index: index, MatcherConfig: MatcherConfig{RawMatcherConfig: RawMatcherConfig{Keyword: keyword}},
		RunnerConfig: config, ParserConfig: ParserConfig{InputBodyMode: inputBodyMode}, Dispatch: DispatchExecutor,
	}, NewHTTPRunner(config), nil)
}

func TestHTTPOnlyChainsUseExecutorAndKeepTheirOperators(t *testing.T) {
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
		text         string
		wantRequests int64
		wantPresent  []string
		wantAbsent   string
		wantExitCode int
	}{
		{text: "lookup ok ; lookup ok", wantRequests: 2, wantPresent: []string{"ok"}},
		{text: "lookup ok && lookup ok", wantRequests: 2, wantPresent: []string{"ok"}},
		{text: "lookup ok || lookup ok", wantRequests: 1, wantPresent: []string{"ok"}},
		{text: "lookup failure || lookup ok", wantRequests: 2, wantPresent: []string{"fail", "ok"}},
		{text: "lookup failure && lookup ok", wantRequests: 1, wantPresent: []string{"fail"}, wantAbsent: "ok", wantExitCode: 1},
		{text: "lookup ok ; unknown", wantRequests: 1, wantPresent: []string{"コマンドが見つかりませんでした"}, wantExitCode: 127},
	} {
		t.Run(tc.text, func(t *testing.T) {
			before := requests.Load()
			config := RunnerConfig{RawRunnerConfig: RawRunnerConfig{Runner: RunnerHTTP, Method: "GET", URL: server.URL + "/*"}}
			command := NewCommand(CommandConfig{
				Index: 1, MatcherConfig: MatcherConfig{RawMatcherConfig: RawMatcherConfig{Keyword: "lookup *"}},
				RunnerConfig: config, ParserConfig: ParserConfig{AllowInChain: true}, Dispatch: DispatchExecutor,
			}, NewHTTPRunner(config), nil)
			parsed := NewCommandSet([]*Command{command}).ResolveInput(tc.text, []int{1})
			input := &CommandInput{Text: tc.text, ConversationID: ConversationID{ChannelID: "C", RootTimestamp: "1"}, ResolvedInput: parsed}
			outputs := make(chan *CommandOutput, 30)
			queued := false
			dispatcher := NewCommandDispatcher(context.Background(), NewExecutor(outputs), outputs, &StdinStore{}, &ConversationLocks{}, func(*CommandInput) bool {
				queued = true
				return true
			})
			if got := dispatcher.Dispatch(input); got != DispatchAccepted || queued {
				t.Fatalf("Dispatch() = %v; queued=%v, want accepted without queue", got, queued)
			}
			dispatcher.Close()
			dispatcher.Wait()
			if got := requests.Load() - before; got != tc.wantRequests {
				t.Fatalf("HTTP requests = %d, want %d", got, tc.wantRequests)
			}
			assertExecutorChainOutput(t, outputs, tc.wantExitCode, tc.wantPresent, tc.wantAbsent)
		})
	}
}

func assertExecutorChainOutput(t *testing.T, outputs chan *CommandOutput, wantExitCode int, wantPresent []string, wantAbsent string) {
	t.Helper()
	var body strings.Builder
	spawned, finished := 0, 0
	var exitCode int
	for len(outputs) > 0 {
		output := <-outputs
		body.WriteString(output.Text)
		if output.Spawned {
			spawned++
		}
		if output.Finished {
			finished++
			exitCode = output.ExitCode
		}
	}
	if spawned != 1 || finished != 1 || exitCode != wantExitCode {
		t.Fatalf("outputs spawned=%d finished=%d exitCode=%d; want one Executor lifecycle with exit code %d", spawned, finished, exitCode, wantExitCode)
	}
	for _, want := range wantPresent {
		if !strings.Contains(body.String(), want) {
			t.Fatalf("output %q does not contain %q", body.String(), want)
		}
	}
	if wantAbsent != "" && strings.Contains(body.String(), wantAbsent) {
		t.Fatalf("output %q contains skipped output %q", body.String(), wantAbsent)
	}
}

func TestHTTPCommandParseErrorUsesDispatcherOutputPipeline(t *testing.T) {
	t.Run("root", func(t *testing.T) { runHTTPParseErrorCase(t, false, "1") })
	t.Run("reply", func(t *testing.T) { runHTTPParseErrorCase(t, true, "2") })
}

func runHTTPParseErrorCase(t *testing.T, isReply bool, timestamp string) {
	t.Helper()
	httpCommand := testHTTPCommand(1, "lookup *", InputBodyStdin)
	systemReply := &struct{ broadcast bool }{broadcast: true}
	httpCommand.config.SystemReplyConfig = systemReply
	outputs := make(chan *CommandOutput, 10)
	queueCalls := 0
	dispatcher := NewCommandDispatcher(context.Background(), nil, outputs, &StdinStore{}, &ConversationLocks{}, func(*CommandInput) bool {
		queueCalls++
		return true
	})
	conversation := ConversationID{ChannelID: "C", RootTimestamp: "1"}
	message := MessageID{ChannelID: "C", Timestamp: timestamp}
	var commands *CommandSet
	if isReply {
		root := NewCommand(CommandConfig{
			Index: 0, MatcherConfig: MatcherConfig{RawMatcherConfig: RawMatcherConfig{Keyword: "agent"}},
			RunnerConfig: RunnerConfig{RawRunnerConfig: RawRunnerConfig{Command: "agent"}},
		}, nil, NewCommandSet([]*Command{httpCommand}))
		commands = NewCommandSet([]*Command{root})
	} else {
		commands = NewCommandSet([]*Command{httpCommand})
	}
	router := NewConversationRouter(&StdinStore{}, commands, func(ConversationID) (RootCommandInput, error) {
		return RootCommandInput{Text: "agent", AllowedCommandIndexes: []int{0}}, nil
	}, dispatcher, 1)
	input := &CommandInput{Text: "lookup '", ConversationID: conversation, MessageID: message, AllowedCommandIndexes: []int{1}}
	result, err := router.Accept(input)
	if err != nil || result != AcceptRouted {
		t.Fatalf("Accept() = %v, %v, want malformed HTTP input routed to output", result, err)
	}
	dispatcher.Close()
	dispatcher.Wait()
	if queueCalls != 0 || len(outputs) != 1 {
		t.Fatalf("queue calls=%d, outputs=%d; want direct parse-error output", queueCalls, len(outputs))
	}
	output := <-outputs
	if output.Text == "" || !output.IsErrOut || output.ExitCode != 2 || output.ReplyConfig != systemReply || output.Spawned || output.Finished || output.MessageID != message {
		t.Fatalf("parse error output = %#v", output)
	}
}

func TestHTTPReplyUsesTheSingleNormalMatchWhenRawCandidateMisses(t *testing.T) {
	for _, tc := range []struct {
		name         string
		standardHTTP bool
	}{
		{name: "exec route"},
		{name: "different HTTP route", standardHTTP: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			httpReply := testHTTPCommand(1, "accept", InputBodyRawStdin)
			var standardReply *Command
			if tc.standardHTTP {
				config := RunnerConfig{RawRunnerConfig: RawRunnerConfig{Runner: RunnerHTTP, Method: "GET", URL: "http://127.0.0.1:1"}}
				standardReply = NewCommand(CommandConfig{
					Index: 2, MatcherConfig: MatcherConfig{RawMatcherConfig: RawMatcherConfig{Keyword: "accept"}},
					RunnerConfig: config, Dispatch: DispatchExecutor,
				}, &fakeRunner{}, nil)
			} else {
				standardReply = NewCommand(CommandConfig{
					Index: 2, MatcherConfig: MatcherConfig{RawMatcherConfig: RawMatcherConfig{Keyword: "accept"}},
					RunnerConfig: RunnerConfig{RawRunnerConfig: RawRunnerConfig{Command: "accept"}},
				}, NewExecRunner(), nil)
			}
			root := NewCommand(CommandConfig{
				Index: 0, MatcherConfig: MatcherConfig{RawMatcherConfig: RawMatcherConfig{Keyword: "agent"}},
				RunnerConfig: RunnerConfig{RawRunnerConfig: RawRunnerConfig{Command: "agent"}},
			}, nil, NewCommandSet([]*Command{httpReply, standardReply}))
			commands := NewCommandSet([]*Command{root})
			var queued []*CommandInput
			dispatcher := newTestCommandDispatcher(context.Background(), 10, func(input *CommandInput) bool {
				queued = append(queued, input)
				return true
			})
			router := NewConversationRouter(&StdinStore{}, commands, nil, dispatcher, 1)
			defer func() { dispatcher.Close(); dispatcher.Wait() }()
			conversation := ConversationID{ChannelID: "C", RootTimestamp: "1"}
			if result, err := router.Accept(&CommandInput{Text: "agent", ConversationID: conversation, MessageID: MessageID{Timestamp: "1"}, AllowedCommandIndexes: []int{0}}); err != nil || result != AcceptRouted {
				t.Fatalf("root Accept() = %v, %v", result, err)
			}
			reply := &CommandInput{Text: `"accept"`, ConversationID: conversation, MessageID: MessageID{Timestamp: "2"}, AllowedCommandIndexes: []int{1, 2}}
			parsed := root.replies.ResolveInput(reply.Text, reply.AllowedCommandIndexes)
			if parsed.Commands[0].Command != standardReply || parsed.SingleCommand() != standardReply {
				t.Fatalf("reply match = (%v, %v), want normal candidate selected", parsed.Commands[0].Command, parsed.SingleCommand())
			}
			result, err := router.Accept(reply)
			if err != nil || result != AcceptRouted {
				t.Fatalf("raw/normal reply = %v, %v", result, err)
			}
			if tc.standardHTTP {
				if len(queued) != 1 {
					t.Fatalf("queued inputs = %d, want only root queued before async HTTP reply", len(queued))
				}
				dispatcher.Wait()
				if calls := standardReply.runner.(*fakeRunner).Calls(); len(calls) != 1 {
					t.Fatalf("HTTP runner calls = %#v, want one async call", calls)
				}
			} else if len(queued) != 2 || queued[1] != reply {
				t.Fatalf("queued inputs = %v, want normal exec reply queued", queued)
			}
		})
	}
}

func TestSameConversationHTTPChainRunsSeriallyWithReplies(t *testing.T) {
	started := make(chan string, 3)
	releaseFirst := make(chan struct{})
	releaseSecond := make(chan struct{})
	releaseThird := make(chan struct{})
	var releaseFirstOnce, releaseSecondOnce, releaseThirdOnce sync.Once
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started <- r.URL.Path
		switch r.URL.Path {
		case "/first":
			<-releaseFirst
		case "/second":
			<-releaseSecond
		case "/third":
			<-releaseThird
		}
		_, _ = io.WriteString(w, r.URL.Path)
	}))
	t.Cleanup(server.Close)

	httpConfig := RunnerConfig{RawRunnerConfig: RawRunnerConfig{Runner: RunnerHTTP, Method: "GET", URL: server.URL + "/*"}}
	httpCommand := NewCommand(CommandConfig{Index: 1, MatcherConfig: MatcherConfig{RawMatcherConfig: RawMatcherConfig{Keyword: "lookup *"}}, RunnerConfig: httpConfig, ParserConfig: ParserConfig{AllowInChain: true}, Dispatch: DispatchExecutor}, NewHTTPRunner(httpConfig), nil)
	root := NewCommand(CommandConfig{Index: 0, MatcherConfig: MatcherConfig{RawMatcherConfig: RawMatcherConfig{Keyword: "agent"}}, RunnerConfig: RunnerConfig{RawRunnerConfig: RawRunnerConfig{Command: "agent"}}}, nil, NewCommandSet([]*Command{httpCommand}))
	commands := NewCommandSet([]*Command{root})
	outputs := make(chan *CommandOutput, 30)
	queued := make(chan *CommandInput, 2)
	dispatcher := NewCommandDispatcher(context.Background(), NewExecutor(outputs), outputs, &StdinStore{}, &ConversationLocks{}, func(input *CommandInput) bool {
		queued <- input
		return true
	})
	router := NewConversationRouter(&StdinStore{}, commands, nil, dispatcher, 1)
	t.Cleanup(func() {
		dispatcher.Close()
		releaseFirstOnce.Do(func() { close(releaseFirst) })
		releaseSecondOnce.Do(func() { close(releaseSecond) })
		releaseThirdOnce.Do(func() { close(releaseThird) })
		dispatcher.Wait()
	})
	conversation := ConversationID{ChannelID: "C", RootTimestamp: "1"}
	acceptRoutedInput(t, router, &CommandInput{Text: "agent", ConversationID: conversation, MessageID: MessageID{Timestamp: "1"}, AllowedCommandIndexes: []int{0}})
	<-queued
	acceptRoutedInput(t, router, &CommandInput{Text: "lookup first ; lookup second", ConversationID: conversation, MessageID: MessageID{Timestamp: "2"}, AllowedCommandIndexes: []int{1}})
	waitForRequestPath(t, started, "/first")
	acceptRoutedInput(t, router, &CommandInput{Text: "lookup third", ConversationID: conversation, MessageID: MessageID{Timestamp: "3"}, AllowedCommandIndexes: []int{1}})
	assertNoHTTPRequestStarted(t, started)
	releaseFirstOnce.Do(func() { close(releaseFirst) })
	waitForRequestPath(t, started, "/second")
	assertNoHTTPRequestStarted(t, started)
	releaseSecondOnce.Do(func() { close(releaseSecond) })
	waitForRequestPath(t, started, "/third")
	releaseThirdOnce.Do(func() { close(releaseThird) })
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
		NewCommand(CommandConfig{Index: 0, MatcherConfig: MatcherConfig{RawMatcherConfig: RawMatcherConfig{Keyword: "lookup"}}, RunnerConfig: RunnerConfig{RawRunnerConfig: RawRunnerConfig{Runner: RunnerHTTP, Method: "GET", URL: server.URL}}, ParserConfig: ParserConfig{AllowInChain: true}, Dispatch: DispatchExecutor}, NewHTTPRunner(RunnerConfig{RawRunnerConfig: RawRunnerConfig{Runner: RunnerHTTP, Method: "GET", URL: server.URL}}), nil),
		NewCommand(CommandConfig{Index: 1, MatcherConfig: MatcherConfig{RawMatcherConfig: RawMatcherConfig{Keyword: "echo"}}, RunnerConfig: RunnerConfig{RawRunnerConfig: RawRunnerConfig{Command: "/bin/echo"}}}, NewExecRunner(), nil),
	})
	queue := make(chan *CommandInput, 1)
	dispatcher := NewCommandDispatcher(ctx, NewExecutor(outputs), outputs, &StdinStore{}, &ConversationLocks{}, func(input *CommandInput) bool {
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
	router := NewConversationRouter(&StdinStore{}, commands, nil, dispatcher, 1)
	if result, err := router.Accept(&CommandInput{Text: "lookup ; lookup", ConversationID: ConversationID{ChannelID: "C", RootTimestamp: "1"}, MessageID: MessageID{Timestamp: "1"}, AllowedCommandIndexes: []int{0}}); err != nil || result != AcceptRouted {
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

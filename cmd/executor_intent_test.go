package cmd

import (
	"context"
	"io"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeCall struct {
	name string
	args []string
}

type fakeRunner struct {
	mu    sync.Mutex
	calls []fakeCall
}

func (r *fakeRunner) CommandContext(_ context.Context, name string, arg ...string) Cmd {
	r.mu.Lock()
	r.calls = append(r.calls, fakeCall{name: name, args: append([]string(nil), arg...)})
	r.mu.Unlock()
	return &fakeCmd{exitCode: 0}
}

func (r *fakeRunner) Calls() []fakeCall {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]fakeCall, len(r.calls))
	copy(out, r.calls)
	return out
}

type fakeCmd struct {
	stdin      io.Reader
	stdout     io.Writer
	stderr     io.Writer
	exitCode   int
	stdoutText string
	stderrText string
}

func (c *fakeCmd) SetStdin(r io.Reader) {
	c.stdin = r
}

func (c *fakeCmd) SetStdout(w io.Writer) {
	c.stdout = w
}

func (c *fakeCmd) SetStderr(w io.Writer) {
	c.stderr = w
}

func (c *fakeCmd) Run(_ int) int {
	if c.stdoutText != "" {
		_, _ = io.WriteString(c.stdout, c.stdoutText)
	}
	if c.stderrText != "" {
		_, _ = io.WriteString(c.stderr, c.stderrText)
	}
	return c.exitCode
}

type contextRunner struct{}

func (contextRunner) CommandContext(_ context.Context, _ string, _ ...string) Cmd {
	return &fakeCmd{stdoutText: "stdout", stderrText: "stderr"}
}

func drainOutputs(ch chan *CommandOutput) []*CommandOutput {
	outputs := make([]*CommandOutput, 0)
	for {
		select {
		case out := <-ch:
			outputs = append(outputs, out)
		default:
			return outputs
		}
	}
}

func runExecutorOnce(
	t *testing.T,
	input string,
	cfgs []*CommandConfig,
) ([]fakeCall, []*CommandOutput) {
	t.Helper()
	rq := make(chan *CommandInput, 1)
	wq := make(chan *CommandOutput, 20)
	runner := &fakeRunner{}
	done := make(chan struct{})
	go func() {
		ExecutorWithRunner(context.Background(), rq, wq, cfgs, func(*CommandConfig) CommandRunner {
			return runner
		})
		close(done)
	}()

	rq <- &CommandInput{Text: input}
	close(rq)

	select {
	case <-done:
	case <-time.After(1 * time.Second):
		t.Fatal("executor did not finish")
	}

	return runner.Calls(), drainOutputs(wq)
}

func testCommandConfigs() []*CommandConfig {
	return []*CommandConfig{
		NewCommandConfig(&Definition{Keyword: "date", Command: "date"}, nil),
		NewCommandConfig(&Definition{Keyword: "deploy *", Command: "deploy *"}, nil),
		NewCommandConfig(&Definition{Keyword: "echo *", Command: "echo *"}, nil),
	}
}

func TestExecutorIntentDetection(t *testing.T) {
	t.Run("single command", testExecutorSingleCommand)
	t.Run("multiple commands with and operator", testExecutorMultipleCommandsWithAnd)
	t.Run("parse error when intent matches", testExecutorParseErrorWhenIntentMatches)
	t.Run("ignore casual message with url", testExecutorIgnoreCasualMessageWithURL)
	t.Run(
		"ignore casual message starting with prefix of valid command",
		testExecutorIgnoreCasualMessageStartingWithPrefix,
	)
	t.Run("ignore casual message with semicolon", testExecutorIgnoreCasualMessageWithSemicolon)
	t.Run(
		"execute valid command and show error for invalid subsequent command",
		testExecutorExecuteValidThenInvalidCommand,
	)
}

func TestExecutorPropagatesConversationContext(t *testing.T) {
	rq := make(chan *CommandInput, 1)
	wq := make(chan *CommandOutput, 20)
	done := make(chan struct{})
	go func() {
		ExecutorWithRunner(
			context.Background(),
			rq,
			wq,
			testCommandConfigs(),
			func(*CommandConfig) CommandRunner {
				return contextRunner{}
			},
		)
		close(done)
	}()

	context := ConversationContext{
		ChannelID:           "C123",
		RootThreadTimestamp: "1700000000.000100",
	}
	rq <- &CommandInput{Text: "date", ConversationContext: context}
	close(rq)
	<-done

	outputs := drainOutputs(wq)
	if len(outputs) != 4 {
		t.Fatalf("expected spawn, stdout, stderr, and finish outputs, got %d", len(outputs))
	}
	for _, output := range outputs {
		if output.ConversationContext != context {
			t.Fatalf("output context = %+v, want %+v", output.ConversationContext, context)
		}
	}
}

func TestExecutorThreadContinuationRequiresThreadMatch(t *testing.T) {
	configs := []*CommandConfig{
		NewCommandConfig(
			&Definition{Keyword: "thread", Command: "thread", Continuation: ContinuationThread},
			nil,
		),
		NewCommandConfig(
			&Definition{Keyword: "plain", Command: "plain"},
			nil,
		),
	}
	for _, tc := range []struct {
		name  string
		input CommandInput
		want  int
	}{
		{name: "mismatch does not run", input: CommandInput{Text: "missing", ThreadContinuation: true}, want: 0},
		{name: "non thread does not run", input: CommandInput{Text: "plain", ThreadContinuation: true}, want: 0},
		{name: "thread runs", input: CommandInput{Text: "thread", ThreadContinuation: true}, want: 1},
		{name: "thread first replays later plain command", input: CommandInput{Text: "thread && plain", ThreadContinuation: true}, want: 2},
		{name: "plain first does not reach later thread command", input: CommandInput{Text: "plain && thread", ThreadContinuation: true}, want: 0},
		{name: "normal command runs", input: CommandInput{Text: "plain"}, want: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls, _ := runExecutorInput(t, &tc.input, configs)
			if len(calls) != tc.want {
				t.Fatalf("calls = %d, want %d", len(calls), tc.want)
			}
		})
	}
}

func TestIsThreadContinuationUsesFirstCommand(t *testing.T) {
	configs := []*CommandConfig{
		NewCommandConfig(
			&Definition{Keyword: "thread", Command: "thread", Continuation: ContinuationThread},
			nil,
		),
		NewCommandConfig(
			&Definition{Keyword: "plain", Command: "plain"},
			nil,
		),
	}
	if !IsThreadContinuation("thread && plain", configs) {
		t.Fatal("thread first command should be eligible")
	}
	if IsThreadContinuation("plain && thread", configs) {
		t.Fatal("plain first command should not be eligible")
	}
}

func runExecutorInput(
	t *testing.T,
	input *CommandInput,
	cfgs []*CommandConfig,
) ([]fakeCall, []*CommandOutput) {
	t.Helper()
	rq := make(chan *CommandInput, 1)
	wq := make(chan *CommandOutput, 20)
	runner := &fakeRunner{}
	done := make(chan struct{})
	go func() {
		ExecutorWithRunner(
			context.Background(),
			rq,
			wq,
			cfgs,
			func(*CommandConfig) CommandRunner { return runner },
		)
		close(done)
	}()
	rq <- input
	close(rq)
	<-done
	return runner.Calls(), drainOutputs(wq)
}

func testExecutorSingleCommand(t *testing.T) {
	t.Helper()
	calls, _ := runExecutorOnce(t, "date", testCommandConfigs())
	if len(calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(calls))
	}
	if calls[0].name != "date" {
		t.Fatalf("expected command 'date', got %q", calls[0].name)
	}
	if len(calls[0].args) != 0 {
		t.Fatalf("expected no args, got %v", calls[0].args)
	}
}

func testExecutorMultipleCommandsWithAnd(t *testing.T) {
	t.Helper()
	calls, _ := runExecutorOnce(t, "deploy foo && deploy bar", testCommandConfigs())
	if len(calls) != 2 {
		t.Fatalf("expected 2 calls, got %d", len(calls))
	}
	if calls[0].name != "deploy" || len(calls[0].args) != 1 || calls[0].args[0] != "foo" {
		t.Fatalf("unexpected first call: %+v", calls[0])
	}
	if calls[1].name != "deploy" || len(calls[1].args) != 1 || calls[1].args[0] != "bar" {
		t.Fatalf("unexpected second call: %+v", calls[1])
	}
}

func testExecutorParseErrorWhenIntentMatches(t *testing.T) {
	t.Helper()
	calls, outputs := runExecutorOnce(t, "echo \"hello", testCommandConfigs())
	if len(calls) != 0 {
		t.Fatalf("expected no calls, got %d", len(calls))
	}
	var errText string
	for _, out := range outputs {
		if out.IsErrOut {
			errText = out.Text
			break
		}
	}
	if errText == "" {
		t.Fatal("expected parse error output")
	}
	if !strings.Contains(errText, "Parse error") {
		t.Fatalf("expected parse error output, got %q", errText)
	}
}

func testExecutorIgnoreCasualMessageWithURL(t *testing.T) {
	t.Helper()
	calls, outputs := runExecutorOnce(
		t,
		"これ確認お願いします <http://example.com>",
		testCommandConfigs(),
	)
	if len(calls) != 0 {
		t.Fatalf("expected no calls, got %d", len(calls))
	}
	if len(outputs) != 0 {
		t.Fatalf("expected no outputs, got %d", len(outputs))
	}
}

func testExecutorIgnoreCasualMessageStartingWithPrefix(t *testing.T) {
	t.Helper()
	calls, outputs := runExecutorOnce(t, "d <http://example.com>", testCommandConfigs())
	if len(calls) != 0 {
		t.Fatalf("expected no calls, got %d", len(calls))
	}
	if len(outputs) != 0 {
		t.Fatalf("expected no outputs, got %d (might be matching by prefix)", len(outputs))
	}
}

func testExecutorIgnoreCasualMessageWithSemicolon(t *testing.T) {
	t.Helper()
	calls, outputs := runExecutorOnce(t, "x ; y", testCommandConfigs())
	if len(calls) != 0 {
		t.Fatalf("expected no calls, got %d", len(calls))
	}
	if len(outputs) != 0 {
		t.Fatalf("expected no outputs, got %d", len(outputs))
	}
}

func testExecutorExecuteValidThenInvalidCommand(t *testing.T) {
	t.Helper()
	calls, outputs := runExecutorOnce(t, "date;x", testCommandConfigs())

	if len(calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(calls))
	}
	if calls[0].name != "date" {
		t.Fatalf("expected command 'date', got %q", calls[0].name)
	}

	var errText string
	for _, out := range outputs {
		if out.IsErrOut {
			errText += out.Text
		}
	}
	if errText == "" {
		t.Fatal("expected error output for invalid command 'x'")
	}
	if !strings.Contains(errText, "x") {
		t.Fatalf("expected error message to contain 'x', got %q", errText)
	}
}

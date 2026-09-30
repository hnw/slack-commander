package cmd

import (
	"context"
	"io"
	"slices"
	"strings"
	"sync"
	"testing"
)

type fakeCall struct {
	name string
	args []string
}

type fakeRunner struct {
	mu     sync.Mutex
	calls  []fakeCall
	inputs []string
}

func (r *fakeRunner) CommandContext(_ context.Context, name string, arg ...string) Cmd {
	r.mu.Lock()
	r.calls = append(r.calls, fakeCall{name: name, args: append([]string(nil), arg...)})
	r.mu.Unlock()
	return &fakeCmd{exitCode: 0, onRun: func(stdin io.Reader) {
		input, _ := io.ReadAll(stdin)
		r.mu.Lock()
		r.inputs = append(r.inputs, string(input))
		r.mu.Unlock()
	}}
}

func (r *fakeRunner) Calls() []fakeCall {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]fakeCall, len(r.calls))
	copy(out, r.calls)
	return out
}

func (r *fakeRunner) Inputs() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.inputs...)
}

type fakeCmd struct {
	stdin      io.Reader
	stdout     io.Writer
	stderr     io.Writer
	exitCode   int
	stdoutText string
	stderrText string
	onRun      func(io.Reader)
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
	if c.onRun != nil && c.stdin != nil {
		c.onRun(c.stdin)
	}
	if c.stdoutText != "" {
		_, _ = io.WriteString(c.stdout, c.stdoutText)
	}
	if c.stderrText != "" {
		_, _ = io.WriteString(c.stderr, c.stderrText)
	}
	return c.exitCode
}

func TestExecutorArgumentBodySendsBodyOnlyToArgv(t *testing.T) {
	config := newTestCommandConfig(&testExecutionConfig{Keyword: "todo *", Command: "todo *", InputBodyMode: InputBodyArgument})
	runner := &fakeRunner{}
	wq := make(chan *CommandOutput, 10)
	executor := NewExecutor(testCommandSet([]*testCommandConfig{config}, func(*testExecutionConfig) CommandRunner {
		return runner
	}), wq)
	executor.Execute(context.Background(), &CommandInput{Text: "todo foo\nbar\n"}, nil)
	if got := runner.Calls(); len(got) != 1 || !slices.Equal(got[0].args, []string{"foo", "\nbar\n"}) {
		t.Fatalf("calls = %#v", got)
	}
	if got := runner.Inputs(); !slices.Equal(got, []string{""}) {
		t.Fatalf("stdin = %#v, want empty", got)
	}
}

func TestExecutorArgumentBodyAppendsOnlyForTrailingWildcard(t *testing.T) {
	tests := []struct {
		name    string
		keyword string
		command string
		input   string
		want    []string
	}{
		{
			name:    "wildcard in the middle",
			keyword: "todo * done",
			command: "todo *",
			input:   "todo foo done\nbar\n",
			want:    []string{"foo"},
		},
		{
			name:    "without wildcard",
			keyword: "todo",
			command: "todo",
			input:   "todo\nbar\n",
			want:    nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := newTestCommandConfig(&testExecutionConfig{Keyword: tt.keyword, Command: tt.command, InputBodyMode: InputBodyArgument})
			calls, _ := runExecutorOnce(t, tt.input, []*testCommandConfig{config})
			if len(calls) != 1 || !slices.Equal(calls[0].args, tt.want) {
				t.Fatalf("calls = %#v, want args %#v", calls, tt.want)
			}
		})
	}
}

func TestExecutorArgumentBodyPassesToHTTPOnlyForTrailingWildcard(t *testing.T) {
	tests := []struct {
		name    string
		keyword string
		input   string
		want    []string
	}{
		{
			name:    "trailing wildcard",
			keyword: "hello *",
			input:   "hello foo bar\nbaz\n",
			want:    []string{"foo bar", "\nbaz\n"},
		},
		{
			name:    "wildcard in the middle",
			keyword: "hello * bar",
			input:   "hello foo bar\nbaz\n",
			want:    []string{"foo"},
		},
		{
			name:    "without wildcard",
			keyword: "hello",
			input:   "hello\nbaz\n",
			want:    nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := newTestCommandConfig(&testExecutionConfig{Keyword: tt.keyword, Runner: "http", InputBodyMode: InputBodyArgument})
			calls, _ := runExecutorOnce(t, tt.input, []*testCommandConfig{config})
			if len(calls) != 1 || calls[0].name != "http" || !slices.Equal(calls[0].args, tt.want) {
				t.Fatalf("calls = %#v, want HTTP args %#v", calls, tt.want)
			}
		})
	}
}

func TestExecutorRejectsChainsContainingDisallowedCommand(t *testing.T) {
	newConfig := func(keyword string, allowInChain bool) *testCommandConfig {
		config := newTestCommandConfig(&testExecutionConfig{Keyword: keyword, Command: keyword, AllowInChain: allowInChain})
		return config
	}
	tests := []struct {
		name      string
		input     string
		configs   []*testCommandConfig
		wantCalls int
	}{
		{
			name: "oneshot chain executes", input: "first ; second", wantCalls: 2,
			configs: []*testCommandConfig{newConfig("first", true), newConfig("second", true)},
		},
		{
			name: "stdin chain rejects", input: "stdin-first ; stdin-second",
			configs: []*testCommandConfig{newConfig("stdin-first", false), newConfig("stdin-second", false)},
		},
		{
			name: "command chain rejects", input: "command-first ; command-second",
			configs: []*testCommandConfig{newConfig("command-first", false), newConfig("command-second", false)},
		},
		{
			name: "oneshot then stdin rejects", input: "first ; stdin-second",
			configs: []*testCommandConfig{newConfig("first", true), newConfig("stdin-second", false)},
		},
		{
			name: "oneshot then command rejects", input: "first ; command-second",
			configs: []*testCommandConfig{newConfig("first", true), newConfig("command-second", false)},
		},
		{
			name: "stdin then oneshot rejects", input: "stdin-first ; second",
			configs: []*testCommandConfig{newConfig("stdin-first", false), newConfig("second", true)},
		},
		{
			name: "command then oneshot rejects", input: "command-first ; second",
			configs: []*testCommandConfig{newConfig("command-first", false), newConfig("second", true)},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			calls, _ := runExecutorOnce(t, tt.input, tt.configs)
			if len(calls) != tt.wantCalls {
				t.Fatalf("calls = %#v, want %d", calls, tt.wantCalls)
			}
		})
	}
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
	cfgs []*testCommandConfig,
) ([]fakeCall, []*CommandOutput) {
	t.Helper()
	wq := make(chan *CommandOutput, 20)
	runner := &fakeRunner{}
	executor := NewExecutor(testCommandSet(cfgs, func(*testExecutionConfig) CommandRunner {
		return runner
	}), wq)
	executor.Execute(context.Background(), &CommandInput{Text: input}, nil)

	return runner.Calls(), drainOutputs(wq)
}

func testCommandConfigs() []*testCommandConfig {
	return []*testCommandConfig{
		newTestCommandConfig(&testExecutionConfig{Keyword: "date", Command: "date", AllowInChain: true}),
		newTestCommandConfig(&testExecutionConfig{Keyword: "deploy *", Command: "deploy *", AllowInChain: true}),
		newTestCommandConfig(&testExecutionConfig{Keyword: "echo *", Command: "echo *", AllowInChain: true}),
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

func TestExecutorInputBodyModes(t *testing.T) {
	tests := []struct {
		name          string
		allowInChain  bool
		inputBodyMode InputBodyMode
		input         string
		wantArgs      []string
		wantCalls     int
	}{
		{name: "stdin body", input: "todo foo\nbar\n", wantArgs: []string{"foo"}, wantCalls: 1, inputBodyMode: InputBodyStdin},
		{name: "argument body", input: "todo foo\nbar\n", wantArgs: []string{"foo", "\nbar\n"}, wantCalls: 1, inputBodyMode: InputBodyArgument},
		{name: "argument body after quoted command", input: "todo \"foo bar\"\nbaz\n", wantArgs: []string{"foo bar", "\nbaz\n"}, wantCalls: 1, inputBodyMode: InputBodyArgument},
		{name: "argument body preserves blank lines", input: "todo\n\n", wantArgs: []string{"\n\n"}, wantCalls: 1, inputBodyMode: InputBodyArgument},
		{name: "argument body ignores empty input", input: "todo\n", wantArgs: nil, wantCalls: 1, inputBodyMode: InputBodyArgument},
		{name: "argument body rejects chains", input: "todo one && todo two", wantCalls: 0, inputBodyMode: InputBodyArgument},
		{name: "disallowed chain rejects", input: "todo one && todo two", wantCalls: 0, inputBodyMode: InputBodyStdin},
		{name: "allowed chain executes", input: "todo one && todo two", wantArgs: []string{"one"}, wantCalls: 2, allowInChain: true, inputBodyMode: InputBodyStdin},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := newTestCommandConfig(&testExecutionConfig{Keyword: "todo *", Command: "todo *"})
			config.AllowInChain = tt.allowInChain
			config.InputBodyMode = tt.inputBodyMode
			calls, _ := runExecutorOnce(t, tt.input, []*testCommandConfig{config})
			if len(calls) != tt.wantCalls {
				t.Fatalf("calls = %#v, want %d calls", calls, tt.wantCalls)
			}
			if tt.wantCalls > 0 && !slices.Equal(calls[0].args, tt.wantArgs) {
				t.Fatalf("first args = %#v, want %#v", calls[0].args, tt.wantArgs)
			}
		})
	}
}

func TestExecutorPropagatesConversationID(t *testing.T) {
	wq := make(chan *CommandOutput, 20)
	executor := NewExecutor(testCommandSet(testCommandConfigs(), func(*testExecutionConfig) CommandRunner {
		return contextRunner{}
	}), wq)

	conversation := ConversationID{
		ChannelID:     "C123",
		RootTimestamp: "1700000000.000100",
	}
	message := MessageID{ChannelID: "C123", Timestamp: "1700000000.000200"}
	executor.Execute(context.Background(), &CommandInput{Text: "date", ConversationID: conversation, MessageID: message}, nil)

	outputs := drainOutputs(wq)
	if len(outputs) != 4 {
		t.Fatalf("expected spawn, stdout, stderr, and finish outputs, got %d", len(outputs))
	}
	for _, output := range outputs {
		if output.ConversationID != conversation {
			t.Fatalf("output context = %+v, want %+v", output.ConversationID, conversation)
		}
		if output.MessageID != message {
			t.Fatalf("output message ID = %+v, want %+v", output.MessageID, message)
		}
	}
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

func TestExecutorSystemMessageReplyConfig(t *testing.T) {
	t.Run("parse error uses matched command setting", func(t *testing.T) {
		systemConfig := &struct{ replyBroadcast bool }{replyBroadcast: false}
		config := newTestCommandConfig(&testExecutionConfig{Keyword: "echo *", Command: "echo *"})
		config.SystemReplyConfig = systemConfig

		_, outputs := runExecutorOnce(t, "echo \"hello", []*testCommandConfig{config})
		for _, output := range outputs {
			if output.IsErrOut {
				if output.ReplyConfig != systemConfig {
					t.Fatalf("system ReplyConfig = %#v, want %#v", output.ReplyConfig, systemConfig)
				}
				return
			}
		}
		t.Fatal("expected parse error output")
	})

	t.Run("unmatched command keeps default setting", func(t *testing.T) {
		config := newTestCommandConfig(&testExecutionConfig{Keyword: "date", Command: "date", AllowInChain: true})
		_, outputs := runExecutorOnce(t, "date && missing", []*testCommandConfig{config})
		for _, output := range outputs {
			if output.IsErrOut {
				if output.ReplyConfig != nil {
					t.Fatalf("system ReplyConfig = %#v, want nil", output.ReplyConfig)
				}
				return
			}
		}
		t.Fatal("expected command not found output")
	})
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

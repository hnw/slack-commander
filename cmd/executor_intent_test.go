package cmd

import (
	"context"
	"errors"
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

func (c *fakeCmd) Run() int {
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
	wq := make(chan *observedOutput, 10)
	commandSet := testCommandSet([]*testCommandConfig{config}, func(*testExecutionConfig) CommandRunner {
		return runner
	})
	observeCommandSet(commandSet, wq)
	executor := NewExecutor()
	input := &CommandInput{Text: "todo foo\nbar\n", AllowedCommandIndexes: []int{0}}
	input.ResolvedInput = commandSet.ResolveInput(input.Text, input.AllowedCommandIndexes)
	executor.Execute(context.Background(), input, nil)
	if got := runner.Calls(); len(got) != 1 || !slices.Equal(got[0].args, []string{"foo", "\nbar\n"}) {
		t.Fatalf("calls = %#v", got)
	}
	if got := runner.Inputs(); !slices.Equal(got, []string{""}) {
		t.Fatalf("stdin = %#v, want empty", got)
	}
}

func TestExecutorRejectsUnresolvedInput(t *testing.T) {
	runner := &fakeRunner{}
	command := newTestCommand(CommandConfig{
		Index: 0, MatcherConfig: MatcherConfig{RawMatcherConfig: RawMatcherConfig{Keyword: "run"}},
		RunnerConfig: RunnerConfig{RawRunnerConfig: RawRunnerConfig{Command: "run"}},
	}, runner, nil)
	set := NewCommandSet([]*Command{command})
	outputs := make(chan *observedOutput, 10)
	observeCommandSet(set, outputs)
	executor := NewExecutor()
	prepared := &CommandInput{Text: "run", AllowedCommandIndexes: []int{0}}
	prepared.ResolvedInput = set.ResolveInput(prepared.Text, prepared.AllowedCommandIndexes)
	executor.Execute(context.Background(), prepared, nil)
	if calls := runner.Calls(); len(calls) != 1 {
		t.Fatalf("prepared input calls = %#v, want one execution", calls)
	}
	drainOutputs(outputs)
	executor.Execute(context.Background(), &CommandInput{Text: "run", AllowedCommandIndexes: []int{0}}, nil)
	if calls := runner.Calls(); len(calls) != 1 {
		t.Fatalf("unprepared input calls = %#v, want unchanged call count", calls)
	}
	if got := drainOutputs(outputs); len(got) != 0 {
		t.Fatalf("unprepared input outputs = %#v, want no output", got)
	}
}

func TestExecutorUsesOnlyListenerAllowedCommandIndexes(t *testing.T) {
	runner := &fakeRunner{}
	command := newTestCommand(CommandConfig{
		Index:         7,
		MatcherConfig: MatcherConfig{RawMatcherConfig: RawMatcherConfig{Keyword: "run"}},
		RunnerConfig:  RunnerConfig{RawRunnerConfig: RawRunnerConfig{Command: "run"}},
		ParserConfig:  ParserConfig{AllowInChain: true},
	}, runner, nil)
	set := NewCommandSet([]*Command{command})
	observeCommandSet(set, make(chan *observedOutput, 10))
	executor := NewExecutor()
	input := &CommandInput{Text: "run", AllowedCommandIndexes: []int{}}
	input.ResolvedInput = set.ResolveInput(input.Text, input.AllowedCommandIndexes)
	executor.Execute(context.Background(), input, nil)
	if calls := runner.Calls(); len(calls) != 0 {
		t.Fatalf("empty candidate set executed commands: %#v", calls)
	}
	input = &CommandInput{Text: "run", AllowedCommandIndexes: []int{7}}
	input.ResolvedInput = set.ResolveInput(input.Text, input.AllowedCommandIndexes)
	executor.Execute(context.Background(), input, nil)
	if calls := runner.Calls(); len(calls) != 1 || calls[0].name != "run" {
		t.Fatalf("allowed candidate calls = %#v, want run", calls)
	}
}

func TestExecutorAppliesGlobalIndexesToReplyCommandSet(t *testing.T) {
	runner := &fakeRunner{}
	reply := newTestCommand(CommandConfig{Index: 12, MatcherConfig: MatcherConfig{RawMatcherConfig: RawMatcherConfig{Keyword: "retry"}}, RunnerConfig: RunnerConfig{RawRunnerConfig: RawRunnerConfig{Command: "retry"}}, ParserConfig: ParserConfig{AllowInChain: true}}, runner, nil)
	secondReply := newTestCommand(CommandConfig{Index: 13, MatcherConfig: MatcherConfig{RawMatcherConfig: RawMatcherConfig{Keyword: "finish"}}, RunnerConfig: RunnerConfig{RawRunnerConfig: RawRunnerConfig{Command: "finish"}}, ParserConfig: ParserConfig{AllowInChain: true}}, runner, nil)
	replySet := NewCommandSet([]*Command{reply, secondReply})
	observeCommandSet(replySet, make(chan *observedOutput, 10))
	executor := NewExecutor()
	input := &CommandInput{Text: "retry ; finish", ResolvedInput: replySet.ResolveInput("retry ; finish", []int{12, 13}), AllowedCommandIndexes: []int{12, 13}}
	executor.Execute(context.Background(), input, nil)
	if calls := runner.Calls(); len(calls) != 2 || calls[0].name != "retry" || calls[1].name != "finish" {
		t.Fatalf("prepared reply calls = %#v, want both reply-set commands", calls)
	}
	// The same reply command must be rejected when its global index is absent.
	executor.Execute(context.Background(), &CommandInput{Text: "retry", ResolvedInput: replySet.ResolveInput("retry", []int{4}), AllowedCommandIndexes: []int{4}}, nil)
	if calls := runner.Calls(); len(calls) != 2 {
		t.Fatalf("disallowed reply executed: %#v", calls)
	}
}

func TestExecutorRunsNormalizedQueuedRawInputAsStdin(t *testing.T) {
	text := `first && second ; third || "unfinished` + "\n  raw body\t"
	runner := &fakeRunner{}
	command := newTestCommand(CommandConfig{
		Index: 2, MatcherConfig: MatcherConfig{RawMatcherConfig: RawMatcherConfig{Keyword: "*"}},
		RunnerConfig: RunnerConfig{RawRunnerConfig: RawRunnerConfig{Command: "capture"}},
		ParserConfig: ParserConfig{InputBodyMode: InputBodyRawStdin},
	}, runner, nil)
	set := NewCommandSet([]*Command{command})
	parsed := set.ResolveInput(text, []int{2})
	wq := make(chan *observedOutput, 10)
	observeCommandSet(set, wq)
	NewExecutor().Execute(context.Background(), &CommandInput{
		Text: text, ResolvedInput: parsed, AllowedCommandIndexes: []int{2},
	}, nil)
	if calls := runner.Calls(); len(calls) != 1 || calls[0].name != "capture" {
		t.Fatalf("calls = %#v, want one raw command", calls)
	}
	if inputs := runner.Inputs(); len(inputs) != 1 || inputs[0] != text {
		t.Fatalf("stdin = %#v, want entire raw text", inputs)
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
			calls, _ := runExecutorOnce(t, tt.input, []*testCommandConfig{config}, []int{0})
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
			calls, _ := runExecutorOnce(t, tt.input, []*testCommandConfig{config}, []int{0})
			if len(calls) != 1 || calls[0].name != "http" || !slices.Equal(calls[0].args, tt.want) {
				t.Fatalf("calls = %#v, want HTTP args %#v", calls, tt.want)
			}
		})
	}
}

func TestExecutorExecutesCommandChains(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantCalls []string
	}{
		{
			name:      "semicolon executes both commands",
			input:     "first ; second",
			wantCalls: []string{"first", "second"},
		},
		{
			name:      "and executes second command after success",
			input:     "first && second",
			wantCalls: []string{"first", "second"},
		},
		{
			name:      "or skips second command after success",
			input:     "first || second",
			wantCalls: []string{"first"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			configs := []*testCommandConfig{
				newTestCommandConfig(&testExecutionConfig{
					Index:        0,
					Keyword:      "first",
					Command:      "first",
					AllowInChain: true,
				}),
				newTestCommandConfig(&testExecutionConfig{
					Index:        1,
					Keyword:      "second",
					Command:      "second",
					AllowInChain: true,
				}),
			}

			calls, _ := runExecutorOnce(t, tt.input, configs, []int{0, 1})

			got := make([]string, 0, len(calls))
			for _, call := range calls {
				got = append(got, call.name)
			}
			if !slices.Equal(got, tt.wantCalls) {
				t.Fatalf("calls = %v, want %v", got, tt.wantCalls)
			}
		})
	}
}

type contextRunner struct{}

func (contextRunner) CommandContext(_ context.Context, _ string, _ ...string) Cmd {
	return &fakeCmd{stdoutText: "stdout", stderrText: "stderr"}
}

func drainOutputs(ch chan *observedOutput) []*observedOutput {
	outputs := make([]*observedOutput, 0)
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
	allowedIndexes []int,
) ([]fakeCall, []*observedOutput) {
	t.Helper()
	wq := make(chan *observedOutput, 20)
	runner := &fakeRunner{}
	commandSet := testCommandSet(cfgs, func(*testExecutionConfig) CommandRunner {
		return runner
	})
	observeCommandSet(commandSet, wq)
	executor := NewExecutor()
	prepared := &CommandInput{Text: input, AllowedCommandIndexes: allowedIndexes}
	prepared.ResolvedInput = commandSet.ResolveInput(input, allowedIndexes)
	executor.Execute(context.Background(), prepared, nil)

	return runner.Calls(), drainOutputs(wq)
}

func testCommandConfigs() []*testCommandConfig {
	return []*testCommandConfig{
		newTestCommandConfig(&testExecutionConfig{Index: 0, Keyword: "date", Command: "date", AllowInChain: true}),
		newTestCommandConfig(&testExecutionConfig{Index: 1, Keyword: "deploy *", Command: "deploy *", AllowInChain: true}),
		newTestCommandConfig(&testExecutionConfig{Index: 2, Keyword: "echo *", Command: "echo *", AllowInChain: true}),
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
	t.Run("report errors for unmatched commands", testExecutorReportsErrorsForUnmatchedCommands)
	t.Run(
		"execute valid command and show error for invalid subsequent command",
		testExecutorExecuteValidThenInvalidCommand,
	)
}

func TestExecutorInputBodyModes(t *testing.T) {
	tests := []struct {
		name          string
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
		{name: "argument body chain execution", input: "todo one && todo two", wantArgs: []string{"one"}, wantCalls: 2, inputBodyMode: InputBodyArgument},
		{name: "stdin body chain execution", input: "todo one && todo two", wantArgs: []string{"one"}, wantCalls: 2, inputBodyMode: InputBodyStdin},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := newTestCommandConfig(&testExecutionConfig{Keyword: "todo *", Command: "todo *"})
			config.AllowInChain = true
			config.InputBodyMode = tt.inputBodyMode
			calls, _ := runExecutorOnce(t, tt.input, []*testCommandConfig{config}, []int{0})
			if len(calls) != tt.wantCalls {
				t.Fatalf("calls = %#v, want %d calls", calls, tt.wantCalls)
			}
			if tt.wantCalls > 0 && !slices.Equal(calls[0].args, tt.wantArgs) {
				t.Fatalf("first args = %#v, want %#v", calls[0].args, tt.wantArgs)
			}
		})
	}
}

func TestExecutorUsesResolvedInputAfterTextAndACLChange(t *testing.T) {
	runner := &fakeRunner{}
	command := newTestCommand(CommandConfig{
		Index: 0, MatcherConfig: MatcherConfig{RawMatcherConfig: RawMatcherConfig{Keyword: "todo *"}},
		RunnerConfig: RunnerConfig{RawRunnerConfig: RawRunnerConfig{Command: "echo *"}},
		ParserConfig: ParserConfig{InputBodyMode: InputBodyArgument},
	}, runner, nil)
	commands := NewCommandSet([]*Command{command})
	parsed := commands.ResolveInput("todo original\nbody", []int{0})
	input := &CommandInput{Text: "unmatched replacement", AllowedCommandIndexes: nil, ResolvedInput: parsed}
	wq := make(chan *observedOutput, 20)
	observeCommandSet(commands, wq)
	NewExecutor().Execute(context.Background(), input, nil)
	if calls := runner.Calls(); len(calls) != 1 || !slices.Equal(calls[0].args, []string{"original", "\nbody"}) {
		t.Fatalf("runner calls = %#v, want prepared args and stdin body", calls)
	}
}

func TestExecutorUsesResolvedParseError(t *testing.T) {
	runner := &fakeRunner{}
	command := newTestCommand(CommandConfig{
		Index: 0, MatcherConfig: MatcherConfig{RawMatcherConfig: RawMatcherConfig{Keyword: "todo *"}},
		RunnerConfig: RunnerConfig{RawRunnerConfig: RawRunnerConfig{Command: "echo *"}},
	}, runner, nil)
	commands := NewCommandSet([]*Command{command})
	parsed := commands.ResolveInput("todo original", []int{0})
	parsed.ParseErr = errors.New("prepared parse error")
	wq := make(chan *observedOutput, 20)
	observeCommandSet(commands, wq)
	NewExecutor().Execute(context.Background(), &CommandInput{
		Text: "unmatched replacement", AllowedCommandIndexes: nil, ResolvedInput: parsed,
	}, nil)
	if calls := runner.Calls(); len(calls) != 0 {
		t.Fatalf("runner calls = %#v, want no execution after prepared parse error", calls)
	}
	if outputs := drainOutputs(wq); len(outputs) != 0 {
		t.Fatalf("Executor outputs = %#v, want parse error handled before execution", outputs)
	}
}

func TestExecutorUsesMatchesResolvedBeforeChainExecution(t *testing.T) {
	secondRunner := &fakeRunner{}
	second := newTestCommand(CommandConfig{
		Index: 1, MatcherConfig: MatcherConfig{RawMatcherConfig: RawMatcherConfig{Keyword: "second *"}},
		RunnerConfig: RunnerConfig{RawRunnerConfig: RawRunnerConfig{Command: "second *"}},
		ParserConfig: ParserConfig{AllowInChain: true},
	}, secondRunner, nil)
	firstRunner := &fakeRunner{}
	first := newTestCommand(CommandConfig{
		Index: 0, MatcherConfig: MatcherConfig{RawMatcherConfig: RawMatcherConfig{Keyword: "first"}},
		RunnerConfig: RunnerConfig{RawRunnerConfig: RawRunnerConfig{Command: "first"}},
		ParserConfig: ParserConfig{AllowInChain: true},
	}, firstRunner, nil)
	commands := NewCommandSet([]*Command{first, second})
	wq := make(chan *observedOutput, 20)
	input := &CommandInput{Text: "first ; second prepared", AllowedCommandIndexes: []int{0, 1}}
	input.ResolvedInput = commands.ResolveInput(input.Text, input.AllowedCommandIndexes)
	if len(input.ResolvedInput.Commands) != 2 || input.ResolvedInput.Commands[1].Command != second || !slices.Equal(input.ResolvedInput.Commands[1].Args, []string{"second", "prepared"}) {
		t.Fatalf("resolved chain = %#v, want second command and prepared args", input.ResolvedInput.Commands)
	}
	second.matcher.keywords = []string{"changed"}
	observeCommandSet(commands, wq)
	commands.commands = []*Command{first}
	input.Text = "first ; changed replacement"
	input.AllowedCommandIndexes = []int{0}
	NewExecutor().Execute(context.Background(), input, nil)
	if calls := secondRunner.Calls(); len(calls) != 1 || !slices.Equal(calls[0].args, []string{"prepared"}) {
		t.Fatalf("second command calls = %#v, want cached match with prepared args", calls)
	}
}

func TestExecutorPropagatesConversationID(t *testing.T) {
	wq := make(chan *observedOutput, 20)
	commandSet := testCommandSet(testCommandConfigs(), func(*testExecutionConfig) CommandRunner {
		return contextRunner{}
	})
	observeCommandSet(commandSet, wq)
	executor := NewExecutor()

	conversation := ConversationID{
		ChannelID:     "C123",
		RootTimestamp: "1700000000.000100",
	}
	message := MessageID{ChannelID: "C123", Timestamp: "1700000000.000200"}
	input := &CommandInput{Text: "date", ConversationID: conversation, MessageID: message, AllowedCommandIndexes: []int{0, 1, 2}}
	input.ResolvedInput = commandSet.ResolveInput(input.Text, input.AllowedCommandIndexes)
	executor.Execute(context.Background(), input, nil)

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
	calls, _ := runExecutorOnce(t, "date", testCommandConfigs(), []int{0, 1, 2})
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
	calls, _ := runExecutorOnce(t, "deploy foo && deploy bar", testCommandConfigs(), []int{0, 1, 2})
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
	calls, outputs := runExecutorOnce(t, "echo \"hello", testCommandConfigs(), []int{0, 1, 2})
	if len(calls) != 0 {
		t.Fatalf("expected no calls, got %d", len(calls))
	}
	if len(outputs) != 0 {
		t.Fatalf("Executor outputs = %#v, want no output for parse error", outputs)
	}
}

func testExecutorIgnoreCasualMessageWithURL(t *testing.T) {
	t.Helper()
	calls, outputs := runExecutorOnce(
		t,
		"これ確認お願いします <http://example.com>",
		testCommandConfigs(),
		[]int{0, 1, 2},
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
	calls, outputs := runExecutorOnce(t, "d <http://example.com>", testCommandConfigs(), []int{0, 1, 2})
	if len(calls) != 0 {
		t.Fatalf("expected no calls, got %d", len(calls))
	}
	if len(outputs) != 0 {
		t.Fatalf("expected no outputs, got %d (might be matching by prefix)", len(outputs))
	}
}

func testExecutorReportsErrorsForUnmatchedCommands(t *testing.T) {
	t.Helper()

	calls, outputs := runExecutorOnce(t, "date ; x ; y", testCommandConfigs(), []int{0, 1, 2})
	if len(calls) != 1 || calls[0].name != "date" {
		t.Fatalf("calls = %#v, want chain owner date", calls)
	}

	var errText string
	for _, out := range outputs {
		if out.IsErrOut {
			errText += out.Text
		}
	}
	if !strings.Contains(errText, "x") {
		t.Fatalf("expected error output for command 'x', got %q", errText)
	}
	if !strings.Contains(errText, "y") {
		t.Fatalf("expected error output for command 'y', got %q", errText)
	}
}

func testExecutorExecuteValidThenInvalidCommand(t *testing.T) {
	t.Helper()
	calls, outputs := runExecutorOnce(t, "date;x", testCommandConfigs(), []int{0, 1, 2})

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

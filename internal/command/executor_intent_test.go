package command

import (
	"context"
	"errors"
	"io"
	"slices"
	"sync"
	"testing"
)

type fakeCall struct {
	wildcard    []string
	hasWildcard bool
}

type fakeRunner struct {
	mu     sync.Mutex
	calls  []fakeCall
	inputs []string
}

func (r *fakeRunner) CommandContext(_ context.Context, wildcard []string, hasWildcard bool) Cmd {
	r.mu.Lock()
	r.calls = append(r.calls, fakeCall{wildcard: append([]string(nil), wildcard...), hasWildcard: hasWildcard})
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

func TestExecutorRejectsUnresolvedInput(t *testing.T) {
	runner := &fakeRunner{}
	command := newTestCommand(testExecutionConfig{
		Index: 0, Keyword: "run", Command: "run",
	}, runner, nil)
	set := NewCommandSet([]*Command{command})
	outputs := make(chan *observedOutput, 10)
	observeCommandSet(set, outputs)
	executor := NewExecutor()
	prepared := &CommandInput{Text: "run", AllowedCommandIndexes: []int{0}}
	prepared.Commands, prepared.ParseErr = set.Resolve(prepared.Text, prepared.AllowedCommandIndexes)
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

func TestExecutorRunsNormalizedQueuedRawInputAsStdin(t *testing.T) {
	text := `first && second ; third || "unfinished` + "\n  raw body\t"
	runner := &fakeRunner{}
	command := newTestCommand(testExecutionConfig{
		Index: 2, Keyword: "*", Command: "capture", InputBodyMode: InputBodyRawStdin,
	}, runner, nil)
	set := NewCommandSet([]*Command{command})
	input := &CommandInput{Text: text, AllowedCommandIndexes: []int{2}}
	input.Commands, input.ParseErr = set.Resolve(text, []int{2})
	wq := make(chan *observedOutput, 10)
	observeCommandSet(set, wq)
	NewExecutor().Execute(context.Background(), input, nil)
	if calls := runner.Calls(); len(calls) != 1 {
		t.Fatalf("calls = %#v, want one raw command", calls)
	}
	if inputs := runner.Inputs(); len(inputs) != 1 || inputs[0] != text {
		t.Fatalf("stdin = %#v, want entire raw text", inputs)
	}
}

func TestExecutorUsesResolvedInputAfterTextAndACLChange(t *testing.T) {
	runner := &fakeRunner{}
	command := newTestCommand(testExecutionConfig{
		Index: 0, Keyword: "todo *", Command: "echo *", InputBodyMode: InputBodyArgument,
	}, runner, nil)
	commands := NewCommandSet([]*Command{command})
	input := &CommandInput{Text: "unmatched replacement"}
	input.Commands, input.ParseErr = commands.Resolve("todo original\nbody", []int{0})
	wq := make(chan *observedOutput, 20)
	observeCommandSet(commands, wq)
	NewExecutor().Execute(context.Background(), input, nil)
	if calls := runner.Calls(); len(calls) != 1 || !slices.Equal(calls[0].wildcard, []string{"original\nbody"}) {
		t.Fatalf("runner calls = %#v, want prepared args and stdin body", calls)
	}
}

func TestExecutorUsesResolvedParseError(t *testing.T) {
	runner := &fakeRunner{}
	command := newTestCommand(testExecutionConfig{
		Index: 0, Keyword: "todo *", Command: "echo *",
	}, runner, nil)
	commands := NewCommandSet([]*Command{command})
	input := &CommandInput{Text: "unmatched replacement", Commands: []ResolvedCommand{{Command: command}}, ParseErr: errors.New("prepared parse error")}
	wq := make(chan *observedOutput, 20)
	observeCommandSet(commands, wq)
	NewExecutor().Execute(context.Background(), input, nil)
	if calls := runner.Calls(); len(calls) != 0 {
		t.Fatalf("runner calls = %#v, want no execution after prepared parse error", calls)
	}
	if outputs := drainOutputs(wq); len(outputs) != 0 {
		t.Fatalf("Executor outputs = %#v, want parse error handled before execution", outputs)
	}
}

func TestExecutorPassesResolvedWildcardAndStdin(t *testing.T) {
	tests := []struct {
		name          string
		inputBodyMode InputBodyMode
		input         string
		wantWildcard  []string
		wantCalls     int
		wantStdin     string
	}{
		{name: "stdin body", input: "todo foo\nbar\n", wantWildcard: []string{"foo"}, wantCalls: 1, wantStdin: "bar\n", inputBodyMode: InputBodyStdin},
		{name: "argument body", input: "todo foo\nbar\n", wantWildcard: []string{"foo\nbar\n"}, wantCalls: 1, inputBodyMode: InputBodyArgument},
		{name: "argument body after quoted command", input: "todo \"foo bar\"\nbaz\n", wantWildcard: []string{"foo bar\nbaz\n"}, wantCalls: 1, inputBodyMode: InputBodyArgument},
		{name: "argument body preserves blank lines", input: "todo\n\n", wantWildcard: []string{"\n\n"}, wantCalls: 1, inputBodyMode: InputBodyArgument},
		{name: "argument body ignores empty input", input: "todo\n", wantWildcard: nil, wantCalls: 1, inputBodyMode: InputBodyArgument},
		{name: "argument body chain execution", input: "todo one && todo two", wantWildcard: []string{"one"}, wantCalls: 2, inputBodyMode: InputBodyArgument},
		{name: "stdin body chain execution", input: "todo one && todo two", wantWildcard: []string{"one"}, wantCalls: 2, inputBodyMode: InputBodyStdin},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runner := &fakeRunner{}
			command := newTestCommand(testExecutionConfig{Keyword: "todo *", InputBodyMode: tt.inputBodyMode, AllowInChain: true}, runner, nil)
			commands, err := NewCommandSet([]*Command{command}).Resolve(tt.input, []int{0})
			if err != nil {
				t.Fatal(err)
			}
			observeCommandSet(NewCommandSet([]*Command{command}), make(chan *observedOutput, 20))
			NewExecutor().Execute(context.Background(), &CommandInput{Commands: commands}, nil)
			calls := runner.Calls()
			if len(calls) != tt.wantCalls {
				t.Fatalf("calls=%#v", calls)
			}
			if !calls[0].hasWildcard || !slices.Equal(calls[0].wildcard, tt.wantWildcard) {
				t.Fatalf("wildcard=%#v, want %q", calls[0], tt.wantWildcard)
			}
			if stdin := runner.Inputs(); len(stdin) != tt.wantCalls || stdin[0] != tt.wantStdin {
				t.Fatalf("stdin=%q, want %q", stdin, tt.wantStdin)
			}
		})
	}
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

func TestExecutorPropagatesConversationID(t *testing.T) {
	wq := make(chan *observedOutput, 20)
	command := newTestCommand(testExecutionConfig{Keyword: "date"}, singleCmdRunner{command: &fakeCmd{stdoutText: "stdout", stderrText: "stderr"}}, nil)
	commandSet := NewCommandSet([]*Command{command})
	observeCommandSet(commandSet, wq)
	executor := NewExecutor()

	conversation := ConversationID{
		ChannelID:     "C123",
		RootTimestamp: "1700000000.000100",
	}
	message := MessageID{ChannelID: "C123", Timestamp: "1700000000.000200"}
	input := &CommandInput{Text: "date", ConversationID: conversation, MessageID: message, AllowedCommandIndexes: []int{0, 1, 2}}
	input.Commands, input.ParseErr = commandSet.Resolve(input.Text, input.AllowedCommandIndexes)
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

func TestExecutorCommandErrorsControlChain(t *testing.T) {
	for _, tc := range []struct {
		op              string
		skipIfFailed    bool
		skipIfSucceeded bool
		wantCalls       int
		wantCode        int
	}{
		{"&&", true, false, 0, 127},
		{"||", false, true, 1, 0},
		{";", false, false, 1, 0},
	} {
		t.Run(tc.op, func(t *testing.T) {
			firstRunner, nextRunner := &fakeRunner{}, &fakeRunner{}
			owner := newTestCommand(testExecutionConfig{}, firstRunner, nil)
			next := newTestCommand(testExecutionConfig{}, nextRunner, nil)
			outputs := make(chan *observedOutput, 10)
			observeCommandSet(NewCommandSet([]*Command{owner}), outputs)
			input := &CommandInput{Commands: []ResolvedCommand{
				{Command: owner},
				{Err: errors.New("resolved command not found")},
				{Command: next, skipIfFailed: tc.skipIfFailed, skipIfSucceeded: tc.skipIfSucceeded},
			}}
			NewExecutor().Execute(context.Background(), input, nil)
			if len(firstRunner.Calls()) != 1 || len(nextRunner.Calls()) != tc.wantCalls {
				t.Fatalf("calls: first=%v next=%v", firstRunner.Calls(), nextRunner.Calls())
			}
			events := drainOutputs(outputs)
			if len(events) != 3 || !events[1].IsErrOut || events[1].Text != "resolved command not found" || !events[2].Finished || events[2].ExitCode != tc.wantCode {
				t.Fatalf("output=%+v", events)
			}
		})
	}
}

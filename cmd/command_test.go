package cmd

import (
	"errors"
	"slices"
	"testing"
)

func TestResolveCommandSelection(t *testing.T) {
	specific := testRuntimeCommand(&testExecutionConfig{Index: 1, Keyword: "deploy *", Command: "deploy *", AllowInChain: true}, nil)
	fallback := testRuntimeCommand(&testExecutionConfig{Index: 2, Keyword: "*", Command: "echo *", AllowInChain: true}, nil)
	duplicate := testRuntimeCommand(&testExecutionConfig{Index: 3, Keyword: "deploy *", Command: "allowed *", AllowInChain: true}, nil)
	for _, tc := range []struct {
		name     string
		text     string
		allowed  []int
		want     *Command
		wildcard []string
	}{
		{"specific", "deploy api", []int{1, 2}, specific, []string{"api"}},
		{"definition order", "deploy api", []int{2, 1}, specific, []string{"api"}},
		{"fallthrough", "deploy api", []int{2}, fallback, []string{"deploy", "api"}},
		{"duplicate ACL", "deploy api", []int{3}, duplicate, []string{"api"}},
		{"nil ACL", "deploy api", nil, nil, nil},
		{"empty ACL", "deploy api", []int{}, nil, nil},
		{"empty wildcard", "deploy", []int{1}, specific, []string{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := NewCommandSet([]*Command{nil, specific, fallback, duplicate}).Resolve(tc.text, tc.allowed)
			if err != nil || len(got) != 1 || got[0].Command != tc.want || !slices.Equal(got[0].Wildcard, tc.wildcard) {
				t.Fatalf("Resolve() = %#v, %v", got, err)
			}
		})
	}
}

func TestResolveChain(t *testing.T) {
	first := testRuntimeCommand(&testExecutionConfig{Index: 1, Keyword: "first *", Command: "first *", AllowInChain: true}, nil)
	third := testRuntimeCommand(&testExecutionConfig{Index: 3, Keyword: "third *", Command: "third *", AllowInChain: true}, nil)
	got, err := NewCommandSet([]*Command{first, third}).Resolve("first one && unknown || third three", []int{1, 3})
	if err != nil || len(got) != 3 || got[0].Command != first || got[1].Command != nil || got[2].Command != third || !got[1].skipIfFailed || !got[2].skipIfSucceeded || (got[1].Err == nil || got[1].Err.Error() != "コマンドが見つかりませんでした: unknown") {
		t.Fatalf("Resolve() = %#v, %v", got, err)
	}
	for _, command := range got {
		if command.StdinText != "" {
			t.Fatal("chain stdin must be empty")
		}
	}
}

func TestResolveBodyModes(t *testing.T) {
	for _, tc := range []struct {
		name     string
		mode     InputBodyMode
		keyword  string
		wildcard []string
		stdin    string
	}{
		{"stdin", InputBodyStdin, "*", []string{"first", "line"}, "second && body"},
		{"nontrailing", InputBodyArgument, "* line", []string{"first"}, ""},
		{"no wildcard", InputBodyArgument, "first line", nil, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			command := testRuntimeCommand(&testExecutionConfig{Keyword: tc.keyword, Command: "echo *", InputBodyMode: tc.mode}, nil)
			got, err := NewCommandSet([]*Command{command}).Resolve("first line\nsecond && body", []int{0})
			if err != nil || len(got) != 1 || got[0].Command != command || !slices.Equal(got[0].Wildcard, tc.wildcard) || got[0].StdinText != tc.stdin {
				t.Fatalf("Resolve() = %#v, %v", got, err)
			}
		})
	}
}

func TestResolveRawStdin(t *testing.T) {
	command := testRuntimeCommand(&testExecutionConfig{Keyword: "*", InputBodyMode: InputBodyRawStdin}, NewStdinReplyRunner())
	for _, text := range []string{"", " \t ", "\"accept\"", "lookup \"unfinished", "first ; second", "first && second", "first || second", "first\nsecond"} {
		got, err := NewCommandSet([]*Command{command}).Resolve(text, []int{0})
		if err != nil || len(got) != 1 || got[0].Command != command || got[0].StdinText != text || got[0].skipIfFailed || got[0].skipIfSucceeded {
			t.Fatalf("Resolve(%q) = %#v, %v", text, got, err)
		}
	}
}

func TestDispatchTarget(t *testing.T) {
	queue := newTestCommand(testExecutionConfig{}, nil, nil)
	executor := newTestCommand(testExecutionConfig{Dispatch: DispatchExecutor}, nil, nil)
	runner := newTestCommand(testExecutionConfig{Dispatch: DispatchRunner}, nil, nil)
	invalid := newTestCommand(testExecutionConfig{Dispatch: 99}, nil, nil)
	for _, tc := range []struct {
		commands []*Command
		want     DispatchMode
		ok       bool
	}{
		{nil, 0, false},
		{[]*Command{nil}, 0, false},
		{[]*Command{queue}, DispatchQueue, true},
		{[]*Command{executor}, DispatchExecutor, true},
		{[]*Command{executor, executor}, DispatchExecutor, true},
		{[]*Command{queue, executor}, DispatchQueue, true},
		{[]*Command{executor, queue}, DispatchQueue, true},
		{[]*Command{nil, executor}, DispatchExecutor, true},
		{[]*Command{queue, nil}, DispatchQueue, true},
		{[]*Command{runner}, DispatchRunner, true},
		{[]*Command{runner, queue}, 0, false},
		{[]*Command{queue, runner}, 0, false},
		{[]*Command{runner, nil}, 0, false},
		{[]*Command{invalid}, 0, false},
	} {
		var resolved []ResolvedCommand
		for _, command := range tc.commands {
			resolved = append(resolved, ResolvedCommand{Command: command})
		}
		got, ok := dispatchTarget(resolved)
		if got != tc.want || ok != tc.ok {
			t.Fatalf("dispatchTarget(%v) = %v,%v", tc.commands, got, ok)
		}
	}
}

func TestNewCommandRequiresOutput(t *testing.T) {
	for _, output := range []CommandOutput{nil, (*testOutputHandler)(nil)} {
		func() {
			defer func() {
				if recover() == nil {
					t.Error("nil output accepted")
				}
			}()
			NewCommand(CommandConfig{}, NewMatcher(MatcherConfig{}), NewExecRunner(RunnerConfig{}), nil, output)
		}()
	}
}

func TestNewCommandKeepsOutputAndReplies(t *testing.T) {
	output := &testOutputHandler{}
	replies := NewCommandSet(nil)
	command := NewCommand(CommandConfig{}, NewMatcher(MatcherConfig{}), NewExecRunner(RunnerConfig{}), replies, output)
	if command.output != output || command.replies != replies {
		t.Fatal("construction dropped output or replies")
	}
}

func TestResolveParseErrorKeepsFirstMatch(t *testing.T) {
	command := testRuntimeCommand(&testExecutionConfig{Keyword: "run", Command: "run", AllowInChain: true}, nil)
	got, err := NewCommandSet([]*Command{command}).Resolve("run &&", []int{0})
	if err == nil || len(got) != 1 || got[0].Command != command {
		t.Fatalf("Resolve() = %#v, %v", got, err)
	}
	input := &CommandInput{Commands: got, ParseErr: errors.New("parse error")}
	if resolvedInputExecutable(input) {
		t.Fatal("parse error executable")
	}
}

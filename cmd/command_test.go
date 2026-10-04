package cmd

import (
	"errors"
	"slices"
	"testing"
)

func TestResolvedInputSingleCommand(t *testing.T) {
	command := newTestCommand(CommandConfig{}, nil, nil)
	for _, tc := range []struct {
		name  string
		input *ResolvedInput
		want  *Command
	}{
		{name: "nil input"},
		{name: "parse error", input: &ResolvedInput{Commands: []ResolvedCommand{{Part: &commandPart{}, Command: command}}, ParseErr: errors.New("parse error")}},
		{name: "no commands", input: &ResolvedInput{}},
		{name: "chain", input: &ResolvedInput{Commands: []ResolvedCommand{{Command: command}, {}}}},
		{name: "single", input: &ResolvedInput{Commands: []ResolvedCommand{{Command: command}}}, want: command},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.input.SingleCommand(); got != tc.want {
				t.Fatalf("SingleCommand() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestResolveInputResolvesSingleCommand(t *testing.T) {
	command := newTestCommand(
		CommandConfig{MatcherConfig: MatcherConfig{RawMatcherConfig: RawMatcherConfig{Keyword: "deploy *"}}, ParserConfig: ParserConfig{AllowInChain: true}, RunnerConfig: RunnerConfig{RawRunnerConfig: RawRunnerConfig{Command: "deploy *"}}},
		NewExecRunner(),
		nil,
	)
	set := NewCommandSet([]*Command{command})

	parsed := set.ResolveInput("deploy api", []int{0})
	if parsed.ParseErr != nil || len(parsed.Commands) != 1 || parsed.Commands[0].Part == nil || !slices.Equal(parsed.Commands[0].Part.args, []string{"deploy", "api"}) || parsed.Commands[0].Command != command || !slices.Equal(parsed.Commands[0].Args, []string{"deploy", "api"}) {
		t.Fatalf("ResolveInput() = (%v), want single resolved command", parsed.Commands)
	}
}

func TestCommandSetMatchReturnsCommandAndArgs(t *testing.T) {
	command := newTestCommand(
		CommandConfig{MatcherConfig: MatcherConfig{RawMatcherConfig: RawMatcherConfig{Keyword: "deploy *"}}, RunnerConfig: RunnerConfig{RawRunnerConfig: RawRunnerConfig{Command: "deploy *"}}},
		NewExecRunner(),
		nil,
	)
	set := NewCommandSet([]*Command{command})
	matched, args := set.Match(newCommandPart("", []string{"deploy", "api"}), []int{0})
	if matched != command || len(args) != 2 || args[0] != "deploy" || args[1] != "api" {
		t.Fatalf("Match() = (%v, %v)", matched, args)
	}
}

func TestResolveInputResolvesEveryChainCommand(t *testing.T) {
	first := newTestCommand(CommandConfig{Index: 0, MatcherConfig: MatcherConfig{RawMatcherConfig: RawMatcherConfig{Keyword: "first *"}}, RunnerConfig: RunnerConfig{RawRunnerConfig: RawRunnerConfig{Command: "run-first *"}}, ParserConfig: ParserConfig{AllowInChain: true}}, nil, nil)
	second := newTestCommand(CommandConfig{Index: 1, MatcherConfig: MatcherConfig{RawMatcherConfig: RawMatcherConfig{Keyword: "second *"}}, RunnerConfig: RunnerConfig{RawRunnerConfig: RawRunnerConfig{Command: "run-second *"}}, ParserConfig: ParserConfig{AllowInChain: true}}, nil, nil)
	third := newTestCommand(CommandConfig{Index: 2, MatcherConfig: MatcherConfig{RawMatcherConfig: RawMatcherConfig{Keyword: "third *"}}, RunnerConfig: RunnerConfig{RawRunnerConfig: RawRunnerConfig{Command: "run-third *"}}, ParserConfig: ParserConfig{AllowInChain: true}}, nil, nil)
	input := NewCommandSet([]*Command{first, second, third}).ResolveInput("first one && second two || third three", []int{0, 1, 2})

	if input.ParseErr != nil || len(input.Commands) != 3 {
		t.Fatalf("ResolveInput() = %#v, want three resolved commands", input)
	}
	wantCommands := []*Command{first, second, third}
	wantParts := [][]string{{"first", "one"}, {"second", "two"}, {"third", "three"}}
	wantArgs := [][]string{{"run-first", "one"}, {"run-second", "two"}, {"run-third", "three"}}
	for i, resolved := range input.Commands {
		if resolved.Part == nil || !slices.Equal(resolved.Part.args, wantParts[i]) || resolved.Command != wantCommands[i] || !slices.Equal(resolved.Args, wantArgs[i]) {
			t.Fatalf("Commands[%d] = %#v, want part %v, command %p, args %v", i, resolved, wantParts[i], wantCommands[i], wantArgs[i])
		}
		if resolved.Part.skipIfSucceeded != (i == 2) || resolved.Part.skipIfFailed != (i == 1) {
			t.Fatalf("Commands[%d] chain flags = (skip-success:%t, skip-failure:%t), want (skip-success:%t, skip-failure:%t)", i, resolved.Part.skipIfSucceeded, resolved.Part.skipIfFailed, i == 2, i == 1)
		}
	}
}

func TestResolveInputKeepsUnmatchedChainPosition(t *testing.T) {
	first := newTestCommand(CommandConfig{Index: 0, MatcherConfig: MatcherConfig{RawMatcherConfig: RawMatcherConfig{Keyword: "first"}}, RunnerConfig: RunnerConfig{RawRunnerConfig: RawRunnerConfig{Command: "first"}}}, nil, nil)
	third := newTestCommand(CommandConfig{Index: 2, MatcherConfig: MatcherConfig{RawMatcherConfig: RawMatcherConfig{Keyword: "third"}}, RunnerConfig: RunnerConfig{RawRunnerConfig: RawRunnerConfig{Command: "third"}}}, nil, nil)
	input := NewCommandSet([]*Command{first, third}).ResolveInput("first && unknown && third", []int{0, 2})

	if input.ParseErr != nil || len(input.Commands) != 3 {
		t.Fatalf("ResolveInput() = %#v, want three command positions", input)
	}
	if input.Commands[0].Command != first || input.Commands[1].Part == nil || !slices.Equal(input.Commands[1].Part.args, []string{"unknown"}) || input.Commands[1].Command != nil || input.Commands[1].Args != nil || !input.Commands[1].Part.skipIfFailed || input.Commands[2].Command != third {
		t.Fatalf("resolved commands = %#v, want first, unmatched middle, third", input.Commands)
	}
}

func TestResolvedInputDispatchTarget(t *testing.T) {
	if DispatchQueue != 0 {
		t.Fatalf("DispatchQueue = %d, want zero value", DispatchQueue)
	}
	queue := newTestCommand(CommandConfig{Dispatch: DispatchQueue}, nil, nil)
	executor := newTestCommand(CommandConfig{Dispatch: DispatchExecutor}, nil, nil)
	runner := newTestCommand(CommandConfig{Dispatch: DispatchRunner}, nil, nil)
	for _, tc := range []struct {
		name   string
		input  *ResolvedInput
		want   DispatchMode
		wantOK bool
	}{
		{name: "empty", input: &ResolvedInput{}},
		{name: "single queue", input: &ResolvedInput{Commands: []ResolvedCommand{{Command: queue}}}, want: DispatchQueue, wantOK: true},
		{name: "single executor", input: &ResolvedInput{Commands: []ResolvedCommand{{Command: executor}}}, want: DispatchExecutor, wantOK: true},
		{name: "executor chain", input: &ResolvedInput{Commands: []ResolvedCommand{{Command: executor}, {Command: executor}}}, want: DispatchExecutor, wantOK: true},
		{name: "unmatched ignored", input: &ResolvedInput{Commands: []ResolvedCommand{{Command: executor}, {}}}, want: DispatchExecutor, wantOK: true},
		{name: "unmatched then executor", input: &ResolvedInput{Commands: []ResolvedCommand{{}, {Command: executor}}}, want: DispatchExecutor, wantOK: true},
		{name: "queue then unmatched", input: &ResolvedInput{Commands: []ResolvedCommand{{Command: queue}, {}}}, want: DispatchQueue, wantOK: true},
		{name: "unmatched then queue", input: &ResolvedInput{Commands: []ResolvedCommand{{}, {Command: queue}}}, want: DispatchQueue, wantOK: true},
		{name: "queue chain", input: &ResolvedInput{Commands: []ResolvedCommand{{Command: queue}, {Command: queue}}}, want: DispatchQueue, wantOK: true},
		{name: "runner", input: &ResolvedInput{Commands: []ResolvedCommand{{Command: runner}}}, want: DispatchRunner, wantOK: true},
		{name: "multiple runners", input: &ResolvedInput{Commands: []ResolvedCommand{{Command: runner}, {Command: runner}}}},
		{name: "runner mixed", input: &ResolvedInput{Commands: []ResolvedCommand{{Command: runner}, {Command: queue}}}},
		{name: "runner with unmatched", input: &ResolvedInput{Commands: []ResolvedCommand{{Command: runner}, {}}}},
		{name: "unmatched with runner", input: &ResolvedInput{Commands: []ResolvedCommand{{}, {Command: runner}}}},
		{name: "unmatched only", input: &ResolvedInput{Commands: []ResolvedCommand{{}, {}}}},
		{name: "zero dispatch is queue", input: &ResolvedInput{Commands: []ResolvedCommand{{Command: newTestCommand(CommandConfig{}, nil, nil)}}}, want: DispatchQueue, wantOK: true},
		{name: "unknown dispatch invalid", input: &ResolvedInput{Commands: []ResolvedCommand{{Command: newTestCommand(CommandConfig{Dispatch: 99}, nil, nil)}}}},
		{name: "queue and executor", input: &ResolvedInput{Commands: []ResolvedCommand{{Command: queue}, {Command: executor}}}, want: DispatchQueue, wantOK: true},
		{name: "executor and queue", input: &ResolvedInput{Commands: []ResolvedCommand{{Command: executor}, {Command: queue}}}, want: DispatchQueue, wantOK: true},
		{name: "nil input"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := tc.input.DispatchTarget()
			if ok != tc.wantOK || (ok && got != tc.want) {
				t.Fatalf("DispatchTarget() = (%v, %v), want (%v, %v)", got, ok, tc.want, tc.wantOK)
			}
		})
	}
}

func TestNewCommandRequiresOutput(t *testing.T) {
	for _, tc := range []struct {
		name   string
		output CommandOutput
	}{
		{name: "nil"},
		{name: "typed nil", output: (*recordingOutputHandler)(nil)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Fatal("nil output handler accepted")
				}
			}()
			NewCommand(CommandConfig{}, nil, nil, tc.output)
		})
	}
}

func TestNewCommandRetainsOutputAndReplies(t *testing.T) {
	output := &recordingOutputHandler{}
	replies := NewCommandSet(nil)
	command := NewCommand(CommandConfig{}, nil, replies, output)
	if command.output != output || command.replies != replies {
		t.Fatal("constructor lost output or reply tree")
	}
}

func TestCommandSetMatchRestrictsIndexesAndAllowsWildcardFallthrough(t *testing.T) {
	restricted := newTestCommand(CommandConfig{Index: 3, MatcherConfig: MatcherConfig{RawMatcherConfig: RawMatcherConfig{Keyword: "deploy"}}, RunnerConfig: RunnerConfig{RawRunnerConfig: RawRunnerConfig{Command: "deploy"}}}, nil, nil)
	wildcard := newTestCommand(CommandConfig{Index: 4, MatcherConfig: MatcherConfig{RawMatcherConfig: RawMatcherConfig{Keyword: "*"}}, RunnerConfig: RunnerConfig{RawRunnerConfig: RawRunnerConfig{Command: "echo *"}}}, nil, nil)
	set := NewCommandSet([]*Command{restricted, wildcard})

	if got, _ := set.Match(newCommandPart("", []string{"deploy"}), []int{4}); got != wildcard {
		t.Fatalf("Match() = %v, want allowed wildcard after denied specific command", got)
	}
	if got, _ := set.Match(newCommandPart("", []string{"deploy"}), []int{3}); got != restricted {
		t.Fatalf("Match() = %v, want allowed command", got)
	}
	if got, _ := set.Match(newCommandPart("", []string{"other"}), []int{}); got != nil {
		t.Fatalf("Match() = %v, want no match for empty candidate set", got)
	}
	if got, _ := set.Match(newCommandPart("", []string{"other"}), []int{4}); got != wildcard {
		t.Fatalf("Match() = %v, want allowed wildcard", got)
	}
}

func TestCommandSetAllowsAuthorizedDuplicateKeyword(t *testing.T) {
	denied := newTestCommand(CommandConfig{Index: 1, MatcherConfig: MatcherConfig{RawMatcherConfig: RawMatcherConfig{Keyword: "status *"}}, RunnerConfig: RunnerConfig{RawRunnerConfig: RawRunnerConfig{Command: "first *"}}}, nil, nil)
	allowed := newTestCommand(CommandConfig{Index: 2, MatcherConfig: MatcherConfig{RawMatcherConfig: RawMatcherConfig{Keyword: "status *"}}, RunnerConfig: RunnerConfig{RawRunnerConfig: RawRunnerConfig{Command: "second *"}}}, nil, nil)
	set := NewCommandSet([]*Command{denied, allowed})

	if got, _ := set.Match(newCommandPart("", []string{"status", "daily"}), []int{2}); got != allowed {
		t.Fatalf("Match() = %v, want allowed command with duplicate keyword", got)
	}
}

func TestCommandSetCandidateSemantics(t *testing.T) {
	first := newTestCommand(CommandConfig{Index: 0, MatcherConfig: MatcherConfig{RawMatcherConfig: RawMatcherConfig{Keyword: "run"}}, RunnerConfig: RunnerConfig{RawRunnerConfig: RawRunnerConfig{Command: "first"}}}, nil, nil)
	second := newTestCommand(CommandConfig{Index: 1, MatcherConfig: MatcherConfig{RawMatcherConfig: RawMatcherConfig{Keyword: "run"}}, RunnerConfig: RunnerConfig{RawRunnerConfig: RawRunnerConfig{Command: "second"}}}, nil, nil)
	set := NewCommandSet([]*Command{first, second})
	for _, tc := range []struct {
		name    string
		allowed []int
		want    *Command
	}{
		{"nil allows no commands", nil, nil},
		{"empty allows no commands", []int{}, nil},
		{"zero is a valid index", []int{0}, first},
		{"only second is allowed", []int{1}, second},
		{"definition order wins over candidate order", []int{1, 0}, first},
		{"unknown index allows no commands", []int{99}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got, _ := set.Match(newCommandPart("", []string{"run"}), tc.allowed); got != tc.want {
				t.Fatalf("Match() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestResolveInputUsesInputBodyMode(t *testing.T) {
	for _, tc := range []struct {
		name string
		mode InputBodyMode
	}{
		{name: "stdin", mode: InputBodyStdin},
		{name: "argument", mode: InputBodyArgument},
	} {
		t.Run(tc.name, func(t *testing.T) {
			command := newTestCommand(CommandConfig{
				Index: 1, MatcherConfig: MatcherConfig{RawMatcherConfig: RawMatcherConfig{Keyword: "*"}},
				RunnerConfig: RunnerConfig{RawRunnerConfig: RawRunnerConfig{Command: "echo *"}}, ParserConfig: ParserConfig{InputBodyMode: tc.mode},
			}, nil, nil)
			set := NewCommandSet([]*Command{command})
			parsed := set.ResolveInput("first line\nsecond && body", []int{1})
			if parsed.Commands[0].Command != command || !slices.Equal(parsed.Commands[0].Args, []string{"echo", "first", "line"}) || parsed.StdinText != "second && body" {
				t.Fatalf("ResolveInput(%v) = (%v, %v, %q), want normal command and stdin", tc.mode, parsed.Commands[0].Command, parsed.Commands[0].Args, parsed.StdinText)
			}
		})
	}
}

func TestResolveInputSelectsCommand(t *testing.T) {
	newCommand := func(index int, keyword, runner string, mode InputBodyMode) *Command {
		return newTestCommand(CommandConfig{
			Index: index, MatcherConfig: MatcherConfig{RawMatcherConfig: RawMatcherConfig{Keyword: keyword}},
			RunnerConfig: RunnerConfig{RawRunnerConfig: RawRunnerConfig{Command: runner}},
			ParserConfig: ParserConfig{InputBodyMode: mode},
		}, nil, nil)
	}
	standard := newCommand(1, "retry *", "standard *", InputBodyStdin)
	raw := newCommand(2, "*", "raw *", InputBodyRawStdin)
	specific := newCommand(3, "deploy *", "deploy *", InputBodyStdin)
	wildcard := newCommand(4, "*", "echo *", InputBodyStdin)
	quotedRaw := newCommand(5, "accept", "raw", InputBodyRawStdin)
	quotedStandard := newCommand(6, "accept", "standard", InputBodyStdin)
	emptyRaw := newCommand(7, "*", "capture *", InputBodyRawStdin)
	quotedRawCompetes := newCommand(9, `"accept"`, "capture", InputBodyRawStdin)

	for _, tc := range []struct {
		name     string
		commands []*Command
		text     string
		allowed  []int
		want     *Command
		wantArgs []string
	}{
		{name: "specific before wildcard", commands: []*Command{specific, wildcard}, text: "deploy api", allowed: []int{3, 4}, want: specific, wantArgs: []string{"deploy", "api"}},
		{name: "raw candidate first by definition order", commands: []*Command{raw, standard}, text: "retry value\nbody", allowed: []int{1, 2}, want: raw, wantArgs: []string{"raw", "retry value", "body"}},
		{name: "normal candidate first by definition order", commands: []*Command{standard, raw}, text: "retry value\nbody", allowed: []int{2, 1}, want: standard, wantArgs: []string{"standard", "value"}},
		{name: "ACL excludes raw candidate", commands: []*Command{raw, standard}, text: "retry value\nbody", allowed: []int{1}, want: standard, wantArgs: []string{"standard", "value"}},
		{name: "ACL allows raw candidate only", commands: []*Command{standard, raw}, text: "retry value\nbody", allowed: []int{2}, want: raw, wantArgs: []string{"raw", "retry value", "body"}},
		{name: "raw candidate misses", commands: []*Command{quotedRaw, quotedStandard}, text: `"accept"`, allowed: []int{5, 6}, want: quotedStandard, wantArgs: []string{"standard"}},
		{name: "empty normal parse selects raw candidate", commands: []*Command{emptyRaw}, text: "", allowed: []int{7}, want: emptyRaw, wantArgs: []string{"capture"}},
		{name: "quoted raw mode wins over normal candidate", commands: []*Command{quotedRawCompetes, quotedStandard}, text: `"accept"`, allowed: []int{6, 9}, want: quotedRawCompetes, wantArgs: []string{"capture"}},
		{name: "empty ACL excludes candidates", commands: []*Command{standard}, text: "retry value", allowed: []int{}, wantArgs: nil},
		{name: "nil ACL excludes candidates", commands: []*Command{standard}, text: "retry value", allowed: nil, wantArgs: nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input := NewCommandSet(tc.commands).ResolveInput(tc.text, tc.allowed)
			if input.ParseErr != nil || len(input.Commands) != 1 || input.Commands[0].Command != tc.want || !slices.Equal(input.Commands[0].Args, tc.wantArgs) {
				t.Fatalf("ResolveInput() = %#v, want command %p and args %v", input, tc.want, tc.wantArgs)
			}
		})
	}
}

func TestResolveInputNormalizesRawStdin(t *testing.T) {
	raw := newTestCommand(CommandConfig{
		Index: 2, MatcherConfig: MatcherConfig{RawMatcherConfig: RawMatcherConfig{Keyword: "*"}},
		RunnerConfig: RunnerConfig{RawRunnerConfig: RawRunnerConfig{Command: "capture"}},
		ParserConfig: ParserConfig{InputBodyMode: InputBodyRawStdin},
	}, nil, nil)
	set := NewCommandSet([]*Command{raw})
	for _, tc := range []struct {
		name string
		text string
	}{
		{name: "quoted", text: `"accept"`},
		{name: "parse error", text: `lookup "unfinished`},
		{name: "semicolon", text: "first ; second"},
		{name: "and", text: "first && second"},
		{name: "or", text: "first || second"},
		{name: "multiline", text: "first line\nsecond line"},
		{name: "whitespace only", text: " \t "},
		{name: "empty", text: ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			text := tc.text
			parsed := set.ResolveInput(text, []int{2})
			if len(parsed.Commands) != 1 || parsed.Commands[0].Command != raw || parsed.ParseErr != nil || parsed.StdinText != text || parsed.Commands[0].Part == nil || !slices.Equal(parsed.Commands[0].Part.args, []string{text}) {
				t.Fatalf("ResolveInput(%q) = %#v, want normalized raw input", text, parsed)
			}
			if parsed.Commands[0].Part.skipIfSucceeded || parsed.Commands[0].Part.skipIfFailed {
				t.Fatalf("raw parsed command inherited chain operators: %#v", parsed.Commands[0])
			}
		})
	}
}

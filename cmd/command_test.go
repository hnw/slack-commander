package cmd

import (
	"slices"
	"testing"
)

func TestCommandSetMatchesSingleRootCommand(t *testing.T) {
	command := newCommand(
		CommandConfig{MatcherConfig: MatcherConfig{RawMatcherConfig: RawMatcherConfig{Keyword: "deploy *"}}, ParserConfig: ParserConfig{AllowInChain: true}, RunnerConfig: RunnerConfig{RawRunnerConfig: RawRunnerConfig{Command: "deploy *"}}, OutputFlushInterval: DefaultOutputFlushInterval},
		NewExecRunner(),
		nil,
	)
	set := NewCommandSet([]*Command{command})

	if got := set.MatchSingle("deploy api", []int{0}); got != command {
		t.Fatalf("MatchSingle() = %v, want command", got)
	}
	if got := set.MatchSingle("deploy api && deploy web", []int{0}); got != nil {
		t.Fatalf("MatchSingle() = %v, want nil for a chain", got)
	}
	matched, args := set.Match(newParsedCommand("", []string{"deploy", "api"}), []int{0})
	if matched != command || len(args) != 2 || args[0] != "deploy" || args[1] != "api" {
		t.Fatalf("Match() = (%v, %v)", matched, args)
	}
}

func TestNewCommandDropsConfigReplies(t *testing.T) {
	command := NewCommand(
		CommandConfig{Replies: []*CommandConfig{{}}},
		NewExecRunner(),
		NewCommandSet(nil),
	)
	if command.config.Replies != nil {
		t.Fatal("runtime command retains config replies")
	}
}

func TestCommandSetMatchRestrictsIndexesAndAllowsWildcardFallthrough(t *testing.T) {
	restricted := NewCommand(CommandConfig{Index: 3, MatcherConfig: MatcherConfig{RawMatcherConfig: RawMatcherConfig{Keyword: "deploy"}}, RunnerConfig: RunnerConfig{RawRunnerConfig: RawRunnerConfig{Command: "deploy"}}}, nil, nil)
	wildcard := NewCommand(CommandConfig{Index: 4, MatcherConfig: MatcherConfig{RawMatcherConfig: RawMatcherConfig{Keyword: "*"}}, RunnerConfig: RunnerConfig{RawRunnerConfig: RawRunnerConfig{Command: "echo *"}}}, nil, nil)
	set := NewCommandSet([]*Command{restricted, wildcard})

	if got, _ := set.Match(newParsedCommand("", []string{"deploy"}), []int{4}); got != wildcard {
		t.Fatalf("Match() = %v, want allowed wildcard after denied specific command", got)
	}
	if got := set.MatchSingle("deploy", []int{4}); got != wildcard {
		t.Fatalf("MatchSingle() = %v, want allowed wildcard after denied specific command", got)
	}
	if got, _ := set.Match(newParsedCommand("", []string{"deploy"}), []int{3}); got != restricted {
		t.Fatalf("Match() = %v, want allowed command", got)
	}
	if got, _ := set.Match(newParsedCommand("", []string{"other"}), []int{}); got != nil {
		t.Fatalf("Match() = %v, want no match for empty candidate set", got)
	}
	if got, _ := set.Match(newParsedCommand("", []string{"other"}), []int{4}); got != wildcard {
		t.Fatalf("Match() = %v, want allowed wildcard", got)
	}
}

func TestCommandSetAllowsAuthorizedDuplicateKeyword(t *testing.T) {
	denied := NewCommand(CommandConfig{Index: 1, MatcherConfig: MatcherConfig{RawMatcherConfig: RawMatcherConfig{Keyword: "status *"}}, RunnerConfig: RunnerConfig{RawRunnerConfig: RawRunnerConfig{Command: "first *"}}}, nil, nil)
	allowed := NewCommand(CommandConfig{Index: 2, MatcherConfig: MatcherConfig{RawMatcherConfig: RawMatcherConfig{Keyword: "status *"}}, RunnerConfig: RunnerConfig{RawRunnerConfig: RawRunnerConfig{Command: "second *"}}}, nil, nil)
	set := NewCommandSet([]*Command{denied, allowed})

	if got, _ := set.Match(newParsedCommand("", []string{"status", "daily"}), []int{2}); got != allowed {
		t.Fatalf("Match() = %v, want allowed command with duplicate keyword", got)
	}
}

func TestCommandSetCandidateSemantics(t *testing.T) {
	first := NewCommand(CommandConfig{Index: 0, MatcherConfig: MatcherConfig{RawMatcherConfig: RawMatcherConfig{Keyword: "run"}}, RunnerConfig: RunnerConfig{RawRunnerConfig: RawRunnerConfig{Command: "first"}}}, nil, nil)
	second := NewCommand(CommandConfig{Index: 1, MatcherConfig: MatcherConfig{RawMatcherConfig: RawMatcherConfig{Keyword: "run"}}, RunnerConfig: RunnerConfig{RawRunnerConfig: RawRunnerConfig{Command: "second"}}}, nil, nil)
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
			if got, _ := set.Match(newParsedCommand("", []string{"run"}), tc.allowed); got != tc.want {
				t.Fatalf("Match() = %v, want %v", got, tc.want)
			}
			if got := set.MatchSingle("run", tc.allowed); got != tc.want {
				t.Fatalf("MatchSingle() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestMatchReplyUsesInputBodyMode(t *testing.T) {
	for _, mode := range []InputBodyMode{InputBodyStdin, InputBodyArgument} {
		command := NewCommand(CommandConfig{
			Index: 1, MatcherConfig: MatcherConfig{RawMatcherConfig: RawMatcherConfig{Keyword: "*"}},
			RunnerConfig: RunnerConfig{RawRunnerConfig: RawRunnerConfig{Command: "echo *"}}, ParserConfig: ParserConfig{InputBodyMode: mode},
		}, nil, nil)
		got, args := NewCommandSet([]*Command{command}).MatchReply("first line\nsecond && body", []int{1})
		if got != command || !slices.Equal(args, []string{"echo", "first", "line"}) {
			t.Fatalf("MatchReply(%v) = (%v, %v), want first-line command", mode, got, args)
		}
	}

	raw := NewCommand(CommandConfig{
		Index: 2, MatcherConfig: MatcherConfig{RawMatcherConfig: RawMatcherConfig{Keyword: `"accept"`}},
		RunnerConfig: RunnerConfig{RawRunnerConfig: RawRunnerConfig{Runner: RunnerExec, Command: "capture"}},
		ParserConfig: ParserConfig{InputBodyMode: InputBodyRawStdin}, DispatchPolicy: DispatchQueued,
	}, nil, nil)
	got, args := NewCommandSet([]*Command{raw}).MatchReply(`"accept"`, []int{2})
	if got != raw || !slices.Equal(args, []string{"capture"}) {
		t.Fatalf("MatchReply(raw mode) = (%v, %v), want mode-selected command", got, args)
	}
}

func TestMatchReplyPreservesDefinitionOrderAndACLAcrossModes(t *testing.T) {
	standard := NewCommand(CommandConfig{
		Index: 1, MatcherConfig: MatcherConfig{RawMatcherConfig: RawMatcherConfig{Keyword: "retry *"}}, RunnerConfig: RunnerConfig{RawRunnerConfig: RawRunnerConfig{Command: "standard *"}},
		ParserConfig: ParserConfig{InputBodyMode: InputBodyStdin},
	}, nil, nil)
	raw := NewCommand(CommandConfig{
		Index: 2, MatcherConfig: MatcherConfig{RawMatcherConfig: RawMatcherConfig{Keyword: "*"}}, RunnerConfig: RunnerConfig{RawRunnerConfig: RawRunnerConfig{Command: "raw *"}},
		ParserConfig: ParserConfig{InputBodyMode: InputBodyRawStdin},
	}, nil, nil)
	set := NewCommandSet([]*Command{standard, raw})
	text := "retry value\nbody"
	if got, _ := set.MatchReply(text, []int{2, 1}); got != standard {
		t.Fatalf("MatchReply() = %v, want first defined matching command", got)
	}
	if got, _ := set.MatchReply(text, []int{2}); got != raw {
		t.Fatalf("MatchReply() = %v, want only allowed raw command", got)
	}
	if got, _ := NewCommandSet([]*Command{raw, standard}).MatchReply(text, []int{1, 2}); got != raw {
		t.Fatalf("MatchReply() = %v, want raw command first by definition order", got)
	}
	if got, _ := set.MatchReply(text, nil); got != nil {
		t.Fatalf("MatchReply(nil ACL) = %v, want nil", got)
	}
	if got, _ := set.MatchReply(text, []int{}); got != nil {
		t.Fatalf("MatchReply(empty ACL) = %v, want nil", got)
	}
}

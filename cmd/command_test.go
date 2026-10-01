package cmd

import "testing"

func TestCommandSetMatchesSingleRootCommand(t *testing.T) {
	command := newCommand(
		CommandConfig{MatcherConfig: MatcherConfig{Keyword: "deploy *"}, RunnerConfig: RunnerConfig{Command: "deploy *"}, ExecutorConfig: ExecutorConfig{AllowInChain: true}, OutputFlushInterval: DefaultOutputFlushInterval},
		NewExecRunner(),
		nil,
	)
	set := NewCommandSet([]*Command{command})

	if got := set.MatchSingle("deploy api", nil); got != command {
		t.Fatalf("MatchSingle() = %v, want command", got)
	}
	if got := set.MatchSingle("deploy api && deploy web", nil); got != nil {
		t.Fatalf("MatchSingle() = %v, want nil for a chain", got)
	}
	matched, args := set.Match(newParsedCommand("", []string{"deploy", "api"}), nil)
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
	restricted := NewCommand(CommandConfig{Index: 3, MatcherConfig: MatcherConfig{Keyword: "deploy"}, RunnerConfig: RunnerConfig{Command: "deploy"}}, nil, nil)
	wildcard := NewCommand(CommandConfig{Index: 4, MatcherConfig: MatcherConfig{Keyword: "*"}, RunnerConfig: RunnerConfig{Command: "echo *"}}, nil, nil)
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
	denied := NewCommand(CommandConfig{Index: 1, MatcherConfig: MatcherConfig{Keyword: "status *"}, RunnerConfig: RunnerConfig{Command: "first *"}}, nil, nil)
	allowed := NewCommand(CommandConfig{Index: 2, MatcherConfig: MatcherConfig{Keyword: "status *"}, RunnerConfig: RunnerConfig{Command: "second *"}}, nil, nil)
	set := NewCommandSet([]*Command{denied, allowed})

	if got, _ := set.Match(newParsedCommand("", []string{"status", "daily"}), []int{2}); got != allowed {
		t.Fatalf("Match() = %v, want allowed command with duplicate keyword", got)
	}
}

func TestCommandSetCandidateSemantics(t *testing.T) {
	first := NewCommand(CommandConfig{Index: 0, MatcherConfig: MatcherConfig{Keyword: "run"}, RunnerConfig: RunnerConfig{Command: "first"}}, nil, nil)
	second := NewCommand(CommandConfig{Index: 1, MatcherConfig: MatcherConfig{Keyword: "run"}, RunnerConfig: RunnerConfig{Command: "second"}}, nil, nil)
	set := NewCommandSet([]*Command{first, second})
	for _, tc := range []struct {
		name    string
		allowed []int
		want    *Command
	}{
		{"nil allows all commands", nil, first},
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

package cmd

import "testing"

func TestCommandSetMatchesSingleRootCommand(t *testing.T) {
	command := newCommand(
		CommandConfig{MatcherConfig: MatcherConfig{Keyword: "deploy *"}, RunnerConfig: RunnerConfig{Command: "deploy *"}, ExecutorConfig: ExecutorConfig{AllowInChain: true}, OutputFlushInterval: DefaultOutputFlushInterval},
		NewExecRunner(),
		nil,
	)
	set := NewCommandSet([]*Command{command})

	if got := set.MatchSingle("deploy api"); got != command {
		t.Fatalf("MatchSingle() = %v, want command", got)
	}
	if got := set.MatchSingle("deploy api && deploy web"); got != nil {
		t.Fatalf("MatchSingle() = %v, want nil for a chain", got)
	}
	matched, args := set.Match(newParsedCommand("", []string{"deploy", "api"}))
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

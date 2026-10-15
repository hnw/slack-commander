package cmd

import (
	"context"
	"strings"
	"testing"
)

func TestResolveRejectsMultilineChain(t *testing.T) {
	command := testRuntimeCommand(&testExecutionConfig{Keyword: "echo *", Command: "/bin/echo *", AllowInChain: true}, nil)
	input := &CommandInput{}
	input.Commands, input.ParseErr = NewCommandSet([]*Command{command}).Resolve("echo first ; echo second\nbody", []int{0})
	if input.ParseErr == nil {
		t.Fatal("複数行チェーンが受理された")
	}
}

func TestRunnerBuildErrorDoesNotChangeMatchedCommand(t *testing.T) {
	for _, kind := range []string{RunnerExec, RunnerCompose} {
		for _, tc := range []struct {
			text    string
			recover bool
		}{
			{"broken", false},
			{"broken && recover", false},
			{"broken || recover", true},
			{"broken ; recover", true},
		} {
			t.Run(kind+"/"+tc.text, func(t *testing.T) {
				definition := RunnerConfig{RawRunnerConfig: RawRunnerConfig{Command: `echo "unfinished`}}
				runner := NewExecRunner(definition)
				if kind == RunnerCompose {
					runner = NewComposeRunner(definition, "/missing-compose-project")
				}
				broken := NewCommand(CommandConfig{Index: 0, ParserConfig: ParserConfig{AllowInChain: true}},
					NewMatcher(MatcherConfig{RawMatcherConfig: RawMatcherConfig{Keyword: "broken"}}), runner, nil, &testOutputHandler{})
				fallback := testRuntimeCommand(&testExecutionConfig{Index: 1, Keyword: "broken", Command: "/bin/echo fallback", AllowInChain: true}, nil)
				recovery := testRuntimeCommand(&testExecutionConfig{Index: 2, Keyword: "recover", Command: "/bin/echo recovery", AllowInChain: true}, nil)
				set := NewCommandSet([]*Command{broken, fallback, recovery})
				outputs := make(chan *observedOutput, 10)
				observeCommandSet(set, outputs)
				commands, err := set.Resolve(tc.text, []int{0, 1, 2})
				if err != nil || commands[0].Command != broken {
					t.Fatalf("Resolve() = %#v, %v", commands, err)
				}
				NewExecutor().Execute(context.Background(), &CommandInput{Commands: commands}, nil)
				var text strings.Builder
				code := -1
				for _, event := range drainOutputs(outputs) {
					text.WriteString(event.Text)
					if event.Finished {
						code = event.ExitCode
					}
				}
				wantCode := 127
				if tc.recover {
					wantCode = 0
				}
				if code != wantCode || strings.Contains(text.String(), "fallback") || strings.Contains(text.String(), "recovery") != tc.recover {
					t.Fatalf("code=%d output=%q", code, text.String())
				}
			})
		}
	}
}

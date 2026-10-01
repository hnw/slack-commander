package cmd

import (
	"reflect"
	"testing"
)

func TestMatcher(t *testing.T) {
	cfgs := []*testCommandConfig{
		{
			testExecutionConfig: &testExecutionConfig{
				Keyword: `ping 8.8.8.8`,
				Command: `ping -c4 8.8.8.8`,
			},
		},
		{
			testExecutionConfig: &testExecutionConfig{
				Keyword: `ping *`,
				Command: `ping * -c4`,
			},
		},
		{
			testExecutionConfig: &testExecutionConfig{
				Keyword: `ping *`,
				Command: `/bin/sh -c "ping *"`,
			},
		},
		{
			testExecutionConfig: &testExecutionConfig{
				Keyword: `echo *`,
				Command: `/bin/echo *`,
			},
		},
		{
			testExecutionConfig: &testExecutionConfig{
				Keyword: `echo *`,
				Command: `/bin/echo "*"`,
			},
		},
		{
			testExecutionConfig: &testExecutionConfig{
				Keyword: `echo *`,
				Command: `/bin/echo '*'`,
			},
		},
		{
			testExecutionConfig: &testExecutionConfig{
				Keyword: `foo * bar`,
				Command: `*`,
			},
		},
		{
			testExecutionConfig: &testExecutionConfig{
				Keyword: `openurl *`,
				Command: `pwopen --no-sandbox *`,
			},
		},
		{
			testExecutionConfig: &testExecutionConfig{
				Keyword: `openurl *`,
				Command: `pwopen --no-sandbox *`,
			},
		},
		{
			testExecutionConfig: &testExecutionConfig{
				Keyword: `openurl`,
				Command: `pwopen --no-sandbox`,
			},
		},
		{
			testExecutionConfig: &testExecutionConfig{
				Keyword: `deploy * bar`,
				Command: `deploy * bar`,
			},
		},
	}
	args := [][]string{
		{`ping`, `8.8.8.8`},
		{`ping`, `-i2`, `8.8.8.8`},
		{`ping`, `-i2`, `8.8.8.8`},
		{`echo`, `foo bar`, `baz`},
		{`echo`, `foo bar`, `baz`},
		{`echo`, `foo bar`, `baz`},
		{`foo`, `baz`, `qux`, `quux`, `bar`},
		{`openurl`},
		{`openurl`, `http://example.com`},
		{`openurl`, `http://example.com`},
		{`deploy`},
	}
	expects := [][]string{
		{`ping`, `-c4`, `8.8.8.8`},
		{`ping`, `-i2`, `8.8.8.8`, `-c4`},
		{`/bin/sh`, `-c`, `ping -i2 8.8.8.8`},
		{`/bin/echo`, `foo bar`, `baz`},
		{`/bin/echo`, `foo bar baz`},
		{`/bin/echo`, `foo\ bar baz`},
		{`baz`, `qux`, `quux`},
		{`pwopen`, `--no-sandbox`},
		{`pwopen`, `--no-sandbox`, `http://example.com`},
		nil,
		nil,
	}

	for i, cfg := range cfgs {
		command := testRuntimeCommand(cfg.testExecutionConfig, nil)
		result := command.match(args[i])

		if !reflect.DeepEqual(result, expects[i]) {
			t.Errorf(
				"Unexpected result for test#%v: expected=%v, actual=%v",
				i+1,
				expects[i],
				result,
			)
		}
	}
}

func TestMatchSingleCommandRejectsChains(t *testing.T) {
	set := testCommandSet([]*testCommandConfig{newTestCommandConfig(&testExecutionConfig{Keyword: "todo *", Command: "todo *"})}, nil)
	if got := set.MatchSingle("todo first", nil); got != set.commands[0] {
		t.Fatalf("MatchSingle() = %v, want configured command", got)
	}
	for _, text := range []string{"todo first && todo second", "todo first || todo second", "todo first ; todo second"} {
		if got := set.MatchSingle(text, nil); got != nil {
			t.Fatalf("MatchSingle(%q) = %v, want nil", text, got)
		}
	}
}

func TestMatcherExpandsAllWildcards(t *testing.T) {
	command := testRuntimeCommand(&testExecutionConfig{Keyword: "echo *", Command: "echo * *"}, nil)
	if got, want := command.match([]string{"echo", "foo bar"}), []string{"echo", "foo bar", "foo bar"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("match() = %#v, want %#v", got, want)
	}
}

func TestMatcherLeavesCommandWildcardsWithoutKeywordWildcard(t *testing.T) {
	command := testRuntimeCommand(&testExecutionConfig{Keyword: "echo", Command: "echo *"}, nil)
	if got, want := command.match([]string{"echo"}), []string{"echo", "*"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("match() = %#v, want %#v", got, want)
	}
}

func TestMatcherDoesNotParseKeywordQuotes(t *testing.T) {
	command := testRuntimeCommand(&testExecutionConfig{Keyword: `foo "bar baz"`, Command: "echo"}, nil)
	if got := command.match([]string{"foo", "bar baz"}); got != nil {
		t.Fatalf("match() = %#v, want nil", got)
	}
}

func TestMatcherDoesNotParseKeywordBackslashes(t *testing.T) {
	command := testRuntimeCommand(&testExecutionConfig{Keyword: `foo\ bar`, Command: "echo"}, nil)
	if got := command.match([]string{"foo bar"}); got != nil {
		t.Fatalf("match() = %#v, want nil", got)
	}
}

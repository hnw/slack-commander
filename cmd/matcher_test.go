package cmd

import (
	"reflect"
	"testing"
)

func TestMatcher(t *testing.T) {
	cfgs := []*CommandConfig{
		{
			ExecutionConfig: &ExecutionConfig{
				Keyword: `ping 8.8.8.8`,
				Command: `ping -c4 8.8.8.8`,
			},
		},
		{
			ExecutionConfig: &ExecutionConfig{
				Keyword: `ping *`,
				Command: `ping * -c4`,
			},
		},
		{
			ExecutionConfig: &ExecutionConfig{
				Keyword: `ping *`,
				Command: `/bin/sh -c "ping *"`,
			},
		},
		{
			ExecutionConfig: &ExecutionConfig{
				Keyword: `echo *`,
				Command: `/bin/echo *`,
			},
		},
		{
			ExecutionConfig: &ExecutionConfig{
				Keyword: `echo *`,
				Command: `/bin/echo "*"`,
			},
		},
		{
			ExecutionConfig: &ExecutionConfig{
				Keyword: `echo *`,
				Command: `/bin/echo '*'`,
			},
		},
		{
			ExecutionConfig: &ExecutionConfig{
				Keyword: `foo * bar`,
				Command: `*`,
			},
		},
		{
			ExecutionConfig: &ExecutionConfig{
				Keyword: `openurl *`,
				Command: `pwopen --no-sandbox *`,
			},
		},
		{
			ExecutionConfig: &ExecutionConfig{
				Keyword: `openurl *`,
				Command: `pwopen --no-sandbox *`,
			},
		},
		{
			ExecutionConfig: &ExecutionConfig{
				Keyword: `openurl`,
				Command: `pwopen --no-sandbox`,
			},
		},
		{
			ExecutionConfig: &ExecutionConfig{
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
		m := newMatcher(cfg.ExecutionConfig)
		result := m.build(args[i])

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
	configs := []*CommandConfig{NewCommandConfig(&ExecutionConfig{Keyword: "todo *", Command: "todo *"})}
	if got := MatchSingleCommand("todo first", configs); got != configs[0] {
		t.Fatalf("MatchSingleCommand() = %v, want configured command", got)
	}
	for _, text := range []string{"todo first && todo second", "todo first || todo second", "todo first ; todo second"} {
		if got := MatchSingleCommand(text, configs); got != nil {
			t.Fatalf("MatchSingleCommand(%q) = %v, want nil", text, got)
		}
	}
}

func TestMatcherExpandsAllWildcards(t *testing.T) {
	m := newMatcher((&CommandConfig{ExecutionConfig: &ExecutionConfig{
		Keyword: "echo *",
		Command: "echo * *",
	}}).ExecutionConfig)
	if got, want := m.build([]string{"echo", "foo bar"}), []string{"echo", "foo bar", "foo bar"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("build() = %#v, want %#v", got, want)
	}
}

func TestMatcherLeavesCommandWildcardsWithoutKeywordWildcard(t *testing.T) {
	m := newMatcher((&CommandConfig{ExecutionConfig: &ExecutionConfig{
		Keyword: "echo",
		Command: "echo *",
	}}).ExecutionConfig)
	if got, want := m.build([]string{"echo"}), []string{"echo", "*"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("build() = %#v, want %#v", got, want)
	}
}

func TestMatcherDoesNotParseKeywordQuotes(t *testing.T) {
	m := newMatcher((&CommandConfig{ExecutionConfig: &ExecutionConfig{
		Keyword: `foo "bar baz"`,
		Command: "echo",
	}}).ExecutionConfig)
	if got := m.build([]string{"foo", "bar baz"}); got != nil {
		t.Fatalf("build() = %#v, want nil", got)
	}
}

func TestMatcherDoesNotParseKeywordBackslashes(t *testing.T) {
	m := newMatcher((&CommandConfig{ExecutionConfig: &ExecutionConfig{
		Keyword: `foo\ bar`,
		Command: "echo",
	}}).ExecutionConfig)
	if got := m.build([]string{"foo bar"}); got != nil {
		t.Fatalf("build() = %#v, want nil", got)
	}
}

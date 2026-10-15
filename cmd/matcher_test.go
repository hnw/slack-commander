package cmd

import (
	"slices"
	"testing"
)

func TestMatcherDoesNotInterpretKeywordSyntax(t *testing.T) {
	for _, tc := range []struct{ keyword, text string }{
		{`foo "bar baz"`, `foo "bar baz"`},
		{`foo\ bar`, `"foo bar"`},
	} {
		matcher := NewMatcher(MatcherConfig{RawMatcherConfig: RawMatcherConfig{Keyword: tc.keyword}})
		if _, ok := matcher.match(tc.text); ok {
			t.Fatalf("keyword=%q unexpectedly matched %q", tc.keyword, tc.text)
		}
	}
}

func TestMatcherArgumentBodyContinuesLastWildcard(t *testing.T) {
	matcher := NewMatcher(MatcherConfig{RawMatcherConfig: RawMatcherConfig{Keyword: "todo *"}, InputBodyMode: InputBodyArgument})
	for _, tc := range []struct {
		text string
		want []string
	}{
		{"todo\n本文", []string{"\n本文"}},
		{"todo foo\n本文", []string{"foo\n本文"}},
		{"todo foo bar\n本文\n", []string{"foo", "bar\n本文\n"}},
	} {
		t.Run(tc.text, func(t *testing.T) {
			got, ok := matcher.match(tc.text)
			if !ok || !got.HasWildcard || !slices.Equal(got.Wildcard, tc.want) || got.StdinText != "" {
				t.Fatalf("match(%q) = %#v, %v; want %q", tc.text, got, ok, tc.want)
			}
		})
	}
}

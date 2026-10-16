package command

import (
	"slices"
	"strings"

	"github.com/mattn/go-shellwords"
)

// Matcher matches parsed keywords to a configured command.
type Matcher struct {
	config   MatcherConfig
	keywords []string
}

// NewMatcher はkeywordの引用符やバックスラッシュを構文として解釈しない。
func NewMatcher(config MatcherConfig) *Matcher {
	return &Matcher{
		config:   config,
		keywords: strings.Fields(config.Keyword),
	}
}

func (m *Matcher) match(text string) (ResolvedCommand, bool) {
	if m.config.InputBodyMode == InputBodyRawStdin {
		_, ok := matchKeywords(m.keywords, []string{text}, slices.Contains(m.keywords, "*"))
		return ResolvedCommand{StdinText: text}, ok
	}
	line, body, _ := strings.Cut(text, "\n")
	tokens, _ := shellwords.NewParser().Parse(line)
	if len(tokens) == 0 {
		tokens = strings.Fields(line)
	}

	hasWildcard := slices.Contains(m.keywords, "*")
	wildcard, ok := matchKeywords(m.keywords, tokens, hasWildcard)
	result := ResolvedCommand{Wildcard: wildcard, HasWildcard: hasWildcard}
	if m.config.InputBodyMode == InputBodyStdin {
		result.StdinText = body
	} else if body != "" && len(m.keywords) > 0 && m.keywords[len(m.keywords)-1] == "*" {
		result.Wildcard = slices.Clone(wildcard)
		if len(result.Wildcard) == 0 {
			result.Wildcard = append(result.Wildcard, "\n"+body)
		} else {
			result.Wildcard[len(result.Wildcard)-1] += "\n" + body
		}
	}
	return result, ok
}

func matchKeywords(template []string, keywords []string, hasWildcard bool) ([]string, bool) {
	if hasWildcard {
		if len(keywords) < len(template)-1 {
			return nil, false
		}
	} else {
		if len(keywords) != len(template) {
			return nil, false
		}
	}

	j := 0
	wildcard := []string{}
	for i, v := range template {
		if v == "*" {
			delta := len(keywords) - len(template)
			wildcard = keywords[i : i+delta+1]
			j = delta
			continue
		}
		idx := i + j
		if idx < 0 || idx >= len(keywords) || v != keywords[idx] {
			return nil, false
		}
	}
	return wildcard, true
}

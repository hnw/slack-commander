package cmd

import (
	"strings"

	"github.com/mattn/go-shellwords"
)

var wildcardReplacer = strings.NewReplacer(
	`\`, `\\`,
	` `, `\ `,
	"\t", "\\\t",
	"`", "\\`",
	`(`, `\(`,
	`)`, `\)`,
	`"`, `\"`,
	`'`, `\'`,
	`;`, `\;`,
	`&`, `\&`,
	`|`, `\|`,
	`<`, `\<`,
	`>`, `\>`,
)

// Matcher matches parsed keywords to a configured command.
type Matcher struct {
	cfg      *CommandConfig
	keywords []string
	runner   CommandRunner
}

// 　CommandConfig.Keyword のワイルドカードを正規表現に書き換えてMatcherを返す
func newMatcher(cfg *CommandConfig) *Matcher {
	parser := shellwords.NewParser()
	keywords, err := parser.Parse(cfg.Keyword)
	if err != nil || parser.Position >= 0 {
		return nil
	}
	return &Matcher{
		cfg:      cfg,
		keywords: keywords,
	}
}

// Matcherの定義に従い、キーワード配列をコマンド配列に変換して返す
// キーワード配列がマッチしなかった場合はnilを返す
func (m *Matcher) build(keywords []string) []string {
	hasWildcard := containsWildcard(m.keywords)
	wildcard, ok := matchKeywords(m.keywords, keywords, hasWildcard)
	if !ok {
		return nil
	}
	runner := strings.ToLower(strings.TrimSpace(m.cfg.Runner))
	if runner == "http" {
		return buildHTTPArgs(hasWildcard, wildcard)
	}
	return buildCommandArgs(m.cfg.Command, hasWildcard, wildcard)
}

func containsWildcard(keywords []string) bool {
	for _, v := range keywords {
		if v == "*" {
			return true
		}
	}
	return false
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
			// wildcard match
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

func buildHTTPArgs(hasWildcard bool, wildcard []string) []string {
	if hasWildcard {
		return []string{"http", strings.Join(wildcard, " ")}
	}
	return []string{"http"}
}

func buildCommandArgs(line string, hasWildcard bool, wildcard []string) []string {
	// コマンド定義中のワイルドカードを展開してからshellwordsでparseする
	if hasWildcard {
		line = expandWildcard(line, wildcard)
	}
	parser := shellwords.NewParser()
	args, err := parser.Parse(line)
	if err != nil || parser.Position >= 0 {
		return nil
	}
	return args
}

func expandWildcard(line string, wildcard []string) string {
	replaced := make([]string, len(wildcard))
	for i, v := range wildcard {
		replaced[i] = wildcardReplacer.Replace(v)
	}
	return strings.Replace(line, "*", strings.Join(replaced, " "), 1)
}

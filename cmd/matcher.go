package cmd

import (
	"strings"

	"github.com/mattn/go-shellwords"
)

type Matcher struct {
	cfg      *CommandConfig
	keywords []string
	runner   CommandRunner
}

//　CommandConfig.Keyword のワイルドカードを正規表現に書き換えてMatcherを返す
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
	hasWildcard := false
	for _, v := range m.keywords {
		if v == "*" {
			hasWildcard = true
			break
		}
	}

	if hasWildcard {
		if len(keywords) < len(m.keywords)-1 {
			return nil
		}
	} else {
		if len(keywords) != len(m.keywords) {
			return nil
		}
	}
	j := 0
	wildcard := []string{}
	for i, v := range m.keywords {
		if v == "*" {
			// wildcard match
			delta := len(keywords) - len(m.keywords)
			wildcard = keywords[i : i+delta+1]
			j = delta
		} else if i+j < 0 || i+j >= len(keywords) || v != keywords[i+j] {
			return nil
		}
	}
	// コマンド定義中のワイルドカードを展開してからshellwordsでparseする
	line := m.cfg.Command
	if hasWildcard {
		replacer := strings.NewReplacer(`\`, `\\`, ` `, `\ `, "\t", "\\\t", "`", "\\`", `(`, `\(`, `)`, `\)`,
			`"`, `\"`, `'`, `\'`, `;`, `\;`, `&`, `\&`, `|`, `\|`, `<`, `\<`, `>`, `\>`)
		replaced := make([]string, len(wildcard))
		for i, v := range wildcard {
			replaced[i] = replacer.Replace(v)
		}
		line = strings.Replace(line, "*", strings.Join(replaced, " "), 1)
	}

	parser := shellwords.NewParser()
	args, err := parser.Parse(line)
	if err != nil || parser.Position >= 0 {
		return nil
	}
	return args
}

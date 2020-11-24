package cmd

import (
	"strings"

	"github.com/mattn/go-shellwords"
)

type commandMatcher struct {
	cfg      *CommandConfig
	keywords []string
}

//　CommandConfig.Keyword のワイルドカードを正規表現に書き換えてcommandMatcherを返す
func newMatcher(cfg *CommandConfig) *commandMatcher {
	parser := shellwords.NewParser()
	keywords, err := parser.Parse(cfg.Keyword)
	if err != nil || parser.Position >= 0 {
		return nil
	}
	return &commandMatcher{
		cfg:      cfg,
		keywords: keywords,
	}
}

// commandMatcherの定義に従い、キーワード配列をコマンド配列に変換して返す
// キーワード配列がマッチしなかった場合はnilを返す
func (m *commandMatcher) build(keywords []string) []string {
	if len(keywords) < len(m.keywords) {
		return nil
	}
	j := 0
	wildcard := []string{}
	for i, v := range m.keywords {
		if v == "*" {
			// wildcard match
			delta := len(keywords) - len(m.keywords)
			wildcard = keywords[i : i+delta+1]
			j = delta
		} else if v != keywords[i+j] {
			return nil
		}
	}
	// コマンド定義中のワイルドカードを展開してからshellwordsでparseする
	line := m.cfg.Command
	if len(wildcard) > 0 {
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

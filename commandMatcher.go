package main

import (
	"errors"
	"regexp"
	"strings"

	"github.com/mattn/go-shellwords"
)

type commandMatcher struct {
	commandConfig
	Pattern *regexp.Regexp
}

//　commandConfig.Keyword のワイルドカードを正規表現に書き換えてcommandMatcherを返す
func newMatcher(cfg *commandConfig) *commandMatcher {
	reWildcard := regexp.MustCompile(`(^| )\\\*( |$)`)
	if cfg.Keyword == "" || strings.Count(cfg.Keyword, "*") >= 2 {
		return nil
	}
	m := &commandMatcher{commandConfig: *cfg}
	pattern := regexp.QuoteMeta(cfg.Keyword)
	pattern = reWildcard.ReplaceAllString(pattern, "(?:^| )(.*)(?: |$)")
	pattern = "^" + pattern + "$"
	re := regexp.MustCompile(pattern)
	m.Pattern = re
	return m
}

// commandMatcherの定義に従って入力メッセージをコマンドの配列として返す
// 入力メッセージがパターンにマッチしなかった場合はerrorを返す
func (m *commandMatcher) replace(msg string) ([]string, error) {
	matches := m.Pattern.FindAllStringSubmatch(msg, 1)
	if matches == nil {
		return nil, errors.New("match error")
	}
	wildcard := ""
	if matches != nil && len(matches[0]) >= 2 {
		wildcard = matches[0][1]
	}

	p := shellwords.NewParser()
	args, err := p.Parse(m.Command)
	if err != nil {
		return nil, err
	}
	if len(args) == 0 {
		return nil, errors.New("failed to parse `command`")
	}
	//　commandConfig.Command のワイルドカード部分をマッチした文字列に書き換え
	var newArgs []string
	if wildcard != "" {
		for i, v := range args {
			if v == "*" {
				if strings.Index(m.Command, `'*'`) != -1 {
					// '*' なら何もしない
					newArgs = args
					break
				}
				newArgs = append(newArgs, args[:i]...)
				if strings.Index(m.Command, `"*"`) != -1 {
					// "*" なら1引数として展開
					newArgs = append(newArgs, wildcard)
				} else {
					// * なら複数引数として展開
					wildcards, err := p.Parse(wildcard)
					if err != nil {
						return []string{}, err
					}
					newArgs = append(newArgs, wildcards...)
				}
				newArgs = append(newArgs, args[i+1:]...)
				break
			}
		}
	}
	if len(newArgs) == 0 {
		newArgs = args
	}
	return newArgs, nil
}

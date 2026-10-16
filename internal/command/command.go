package command

import (
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/mattn/go-shellwords"
)

// RawMatcherConfig decodes matcher settings from TOML.
type RawMatcherConfig struct {
	Keyword string `toml:"keyword"`
}

// MatcherConfig contains the settings needed to match a command.
type MatcherConfig struct {
	RawMatcherConfig
	InputBodyMode InputBodyMode
}

// ParserConfig contains input parsing policy.
type ParserConfig struct {
	AllowInChain bool
}

// RawRunnerConfig decodes runner settings from TOML.
type RawRunnerConfig struct {
	Runner  string            `toml:"runner"`
	Command string            `toml:"command"`
	Method  string            `toml:"method"`
	URL     string            `toml:"url"`
	Headers map[string]string `toml:"headers"`
	Body    string            `toml:"body"`
}

// RunnerConfig contains the runner-specific command definition.
type RunnerConfig struct {
	RawRunnerConfig
}

// ExecutorConfig contains execution policy after a command has matched.
type ExecutorConfig struct {
	Timeout          time.Duration
	StdinIdleTimeout time.Duration
	TTY              bool
	InteractiveStdin bool
}

// CommandConfig は解決済みのcommand実行設定を保持する。
type CommandConfig struct {
	Index int
	ParserConfig
	ExecutorConfig
	Dispatch DispatchMode
}

// DispatchMode defines how a matched command is dispatched.
type DispatchMode int

const (
	// DispatchQueue routes the input through the worker queue to the Executor.
	DispatchQueue DispatchMode = iota
	// DispatchExecutor bypasses the worker queue and routes the input to the Executor.
	DispatchExecutor
	// DispatchRunner bypasses the Executor and invokes the runner directly.
	DispatchRunner
)

// Command is a runtime command with its output and reply CommandSet.
type Command struct {
	output  CommandOutput
	config  CommandConfig
	matcher Matcher
	runner  CommandRunner
	replies *CommandSet
}

// NewCommand requires a CommandOutput at construction time.
func NewCommand(config CommandConfig, matcher *Matcher, runner CommandRunner, replies *CommandSet, output CommandOutput) *Command {
	if output == nil {
		panic("command.NewCommand requires a CommandOutput")
	}
	switch value := reflect.ValueOf(output); value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		if value.IsNil() {
			panic("command.NewCommand requires a CommandOutput")
		}
	}
	return &Command{
		config:  config,
		output:  output,
		matcher: *matcher,
		runner:  runner,
		replies: replies,
	}
}

// CommandSet は先に定義された許可済み候補を優先する。
type CommandSet struct{ commands []*Command }

// ResolvedCommand のErrは入力全体を停止せず、チェーン内の実行失敗として扱う。
type ResolvedCommand struct {
	skipIfSucceeded bool
	skipIfFailed    bool
	Err             error
	Command         *Command
	Wildcard        []string
	HasWildcard     bool
	StdinText       string
}

func NewCommandSet(commands []*Command) *CommandSet {
	return &CommandSet{commands: commands}
}

// Resolve は構文エラーでも先頭の出力先へ通知できるよう、部分的な照合結果を返す。
func (s *CommandSet) Resolve(text string, allowedIndexes []int) ([]ResolvedCommand, error) {
	line, _, multiline := strings.Cut(text, "\n")
	parts, parseErr := parseCommands(line)
	first := s.match(text, allowedIndexes)
	if first.Command != nil && first.Command.matcher.config.InputBodyMode == InputBodyRawStdin {
		return []ResolvedCommand{first}, nil
	}
	resolved := make([]ResolvedCommand, len(parts))
	for i, part := range parts {
		result := first
		if i > 0 {
			result = s.match(part.text, allowedIndexes)
		}
		if result.Command == nil {
			result.Err = fmt.Errorf("コマンドが見つかりませんでした: %s", strings.Join(part.args, " "))
		}
		result.skipIfSucceeded, result.skipIfFailed = part.skipIfSucceeded, part.skipIfFailed
		resolved[i] = result
	}
	if len(parts) > 1 {
		for i := range resolved {
			resolved[i].StdinText = ""
		}
		if multiline {
			return resolved, errors.New("コマンドチェーンでは複数行入力を使用できません")
		}
	}
	return resolved, parseErr
}

func (s *CommandSet) match(text string, allowedIndexes []int) ResolvedCommand {
	if s == nil {
		return ResolvedCommand{}
	}
	for _, command := range s.commands {
		if command == nil || !slices.Contains(allowedIndexes, command.config.Index) {
			continue
		}
		result, ok := command.matcher.match(text)
		if ok {
			result.Command = command
			return result
		}
	}
	return ResolvedCommand{}
}

func parseCommands(cmdMsg string) ([]*commandPart, error) {
	cmds, err := parse(cmdMsg)
	// パースに完全に失敗した場合のフォールバック（クォーテーション忘れ等）
	if len(cmds) == 0 {
		fields := strings.Fields(cmdMsg)
		if len(fields) > 0 {
			cmds = append(cmds, newCommandPart("", fields))
		}
	}
	return cmds, err
}

type commandPart struct {
	skipIfSucceeded bool
	skipIfFailed    bool
	args            []string
	text            string
}

func newCommandPart(op string, args []string) *commandPart {
	skipIfSucceeded := false
	skipIfFailed := false
	switch op {
	case "&&":
		skipIfFailed = true
	case "||":
		skipIfSucceeded = true
	}
	return &commandPart{
		skipIfSucceeded: skipIfSucceeded,
		skipIfFailed:    skipIfFailed,
		args:            args,
		text:            strings.Join(args, " "),
	}
}

func parse(line string) ([]*commandPart, error) {
	parser := shellwords.NewParser()
	prevOperator := "" // 「;」相当
	cmds := make([]*commandPart, 0)

	for {
		args, err := parser.Parse(line)
		if len(args) == 0 {
			if prevOperator == "" {
				runes := []rune(line)
				prevOperator = string(runes[:min(2, len(runes))])
			}
			err = errors.New("Parse error near `" + prevOperator + "'")
		}
		if err != nil {
			return cmds, err
		}
		part := newCommandPart(prevOperator, args)
		part.text = strings.TrimSpace(line)
		if parser.Position >= 0 {
			part.text = strings.TrimSpace(string([]rune(line)[:parser.Position]))
		}
		cmds = append(cmds, part)
		if parser.Position < 0 {
			// 文字列末尾までparseした
			return cmds, nil
		}
		i := len(string([]rune(line)[:parser.Position]))
		token := line[i:]
		operators := []string{";", "&&", "||"}
		prevOperator = ""
		for _, op := range operators {
			if strings.HasPrefix(token, op) {
				i += len(op)
				prevOperator = op
				break
			}
		}
		// 次のイテレーションでオペレータの次の文字列からparse開始
		// 未対応のオペレータだった場合は次のイテレーションでparse error
		line = string(line[i:])
	}
}

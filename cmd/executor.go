package cmd

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mattn/go-shellwords"
)

// CommandInput はPubSubからの情報をExecutorに引き渡す構造体
type CommandInput struct {
	ReplyInfo interface{} // PubSubの返信に必要な構造体（PubSubの種類ごとにキャストして利用する）
	Text      string      // 起動コマンド平文
}

// CommandOutput はExecutorからの実行結果を引き渡してPubSubに書き出すための構造体
type CommandOutput struct {
	ReplyInfo   interface{}
	ReplyConfig interface{}
	Text        string // コマンドからのテキスト出力（ImageData と排他）
	ImageData   []byte // sixel を変換した PNG バイト列（Text と排他）
	IsErrOut    bool
	Spawned     bool
	Finished    bool
	ExitCode    int
}

type Definition struct {
	Timeout int
	Keyword string
	Command string
	Runner  string
	Method  string
	URL     string
	Headers map[string]string
	Body    string
}

type CommandConfig struct {
	*Definition
	ReplyConfig interface{} //*pubsub.ReplyConfig
}

func NewCommandConfig(def *Definition, replyConfig interface{}) *CommandConfig {
	return &CommandConfig{
		Definition:  def,
		ReplyConfig: replyConfig,
	}
}

func Executor(rq chan *CommandInput, wq chan *CommandOutput, cfgs []*CommandConfig) {
	ExecutorWithRunner(rq, wq, cfgs, nil)
}

// RunnerFactory returns a runner for the given command config.
type RunnerFactory func(cfg *CommandConfig) CommandRunner

// ExecutorWithRunner runs commands using runners provided by runnerFactory.
func ExecutorWithRunner(
	rq chan *CommandInput,
	wq chan *CommandOutput,
	cfgs []*CommandConfig,
	runnerFactory RunnerFactory,
) {
	runnerFactory = normalizeRunnerFactory(runnerFactory)
	matchers := buildMatchers(cfgs, runnerFactory)

	for input := range rq {
		cmdMsg, stdinText := splitCommandInput(input.Text)
		cmds, parseErr := parseCommands(cmdMsg)
		_ = executeCommands(cmds, parseErr, stdinText, input, matchers, wq)
	}
}

func normalizeRunnerFactory(runnerFactory RunnerFactory) RunnerFactory {
	if runnerFactory != nil {
		return runnerFactory
	}
	return func(*CommandConfig) CommandRunner {
		return NewExecRunner()
	}
}

func buildMatchers(cfgs []*CommandConfig, runnerFactory RunnerFactory) []*Matcher {
	matchers := make([]*Matcher, 0, len(cfgs))
	for _, cfg := range cfgs {
		matcher := newMatcher(cfg)
		if matcher == nil {
			continue
		}
		runner := runnerFactory(cfg)
		if runner == nil {
			runner = NewExecRunner()
		}
		matcher.runner = runner
		matchers = append(matchers, matcher)
	}
	return matchers
}

func splitCommandInput(text string) (string, string) {
	msgArr := strings.SplitN(text, "\n", 2)
	cmdMsg := msgArr[0]
	stdinText := ""
	if len(msgArr) >= 2 {
		// メッセージが複数行だった場合、1行目をコマンド、2行目以降を標準入力として扱う
		stdinText = msgArr[1]
	}
	return cmdMsg, stdinText
}

func parseCommands(cmdMsg string) ([]*parsedCommand, error) {
	cmds, err := parse(cmdMsg)
	// パースに完全に失敗した場合のフォールバック（クォーテーション忘れ等）
	if len(cmds) == 0 {
		fields := strings.Fields(cmdMsg)
		if len(fields) > 0 {
			cmds = append(cmds, newParsedCommand("", fields))
		}
	}
	return cmds, err
}

func executeCommands(
	cmds []*parsedCommand,
	parseErr error,
	stdinText string,
	input *CommandInput,
	matchers []*Matcher,
	wq chan *CommandOutput,
) int {
	ret := 0
	for i, cmd := range cmds {
		if shouldSkipCommand(cmd, ret) {
			continue
		}
		ret = -1
		m, args := findMatchedMatcher(cmd, matchers)
		if m == nil {
			if i == 0 {
				// キーワードにマッチしなかったらparse errorがあっても表示せず終了
				return 0
			}
			ret = writeCommandNotFound(wq, input, cmd)
			continue
		}
		if i == 0 {
			// コマンド実行開始を通知
			wq <- &CommandOutput{
				ReplyInfo: input.ReplyInfo,
				Spawned:   true,
			}
			// 関数を抜ける時に必ず終了通知を送る
			defer func() {
				wq <- &CommandOutput{
					ReplyInfo: input.ReplyInfo,
					Finished:  true,
					ExitCode:  ret,
				}
			}()
		}
		if parseErr != nil {
			// parse errorありで1つ目のコマンドがキーワードマッチした場合
			// エラー表示して処理全体を終了
			ret = writeParseError(wq, input, parseErr)
			return ret
		}
		ret = runMatchedCommand(m, args, stdinText, input, wq)
	}
	return ret
}

func shouldSkipCommand(cmd *parsedCommand, ret int) bool {
	if ret == 0 && cmd.skipIfSucceeded {
		return true
	}
	return ret != 0 && cmd.skipIfFailed
}

func writeParseError(wq chan *CommandOutput, input *CommandInput, parseErr error) int {
	syserr := newErrWriter(wq, input.ReplyInfo, nil)
	_, _ = fmt.Fprintf(syserr, "%v", parseErr)
	_ = syserr.Flush()
	return 2
}

func writeCommandNotFound(wq chan *CommandOutput, input *CommandInput, cmd *parsedCommand) int {
	syserr := newErrWriter(wq, input.ReplyInfo, nil)
	_, _ = fmt.Fprintf(syserr, "コマンドが見つかりませんでした: %v", strings.Join(cmd.args, " "))
	_ = syserr.Flush()
	return 127
}

func runMatchedCommand(
	m *Matcher,
	args []string,
	stdinText string,
	input *CommandInput,
	wq chan *CommandOutput,
) int {
	var ctx context.Context
	var cancel context.CancelFunc
	if m.cfg.Timeout > 0 {
		ctx, cancel = context.WithTimeout(
			context.Background(),
			time.Duration(m.cfg.Timeout)*time.Second,
		)
	} else {
		ctx, cancel = context.WithCancel(context.Background())
	}
	defer cancel()

	execCmd := m.runner.CommandContext(ctx, args[0], args[1:]...)
	execCmd.SetStdin(strings.NewReader(stdinText))
	stdout := newStdWriter(wq, input.ReplyInfo, m.cfg.ReplyConfig)
	stderr := newErrWriter(wq, input.ReplyInfo, m.cfg.ReplyConfig)
	execCmd.SetStdout(stdout)
	execCmd.SetStderr(stderr)
	ret := execCmd.Run(m.cfg.Timeout)
	_ = stdout.Flush()
	_ = stderr.Flush()

	return ret
}

type parsedCommand struct {
	skipIfSucceeded bool
	skipIfFailed    bool
	args            []string
}

func newParsedCommand(op string, args []string) *parsedCommand {
	skipIfSucceeded := false
	skipIfFailed := false
	if op == "&&" {
		skipIfFailed = true
	} else if op == "||" {
		skipIfSucceeded = true
	}
	return &parsedCommand{
		skipIfSucceeded: skipIfSucceeded,
		skipIfFailed:    skipIfFailed,
		args:            args,
	}
}

func parse(line string) ([]*parsedCommand, error) {
	parser := shellwords.NewParser()
	prevOperator := "" // 「;」相当
	cmds := make([]*parsedCommand, 0)

	for {
		args, err := parser.Parse(line)
		if len(args) == 0 {
			if prevOperator == "" {
				end := 2
				if len(line) < 2 {
					end = len(line)
				}
				prevOperator = string([]rune(line)[0:end])
			}
			err = errors.New("Parse error near `" + prevOperator + "'")
		}
		if err != nil {
			return cmds, err
		}
		cmds = append(cmds, newParsedCommand(prevOperator, args))
		if parser.Position < 0 {
			// 文字列末尾までparseした
			return cmds, nil
		}
		i := parser.Position
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

// findMatchedMatcher はマッチャーと構築された引数を返します。
// マッチしない場合は nil, nil を返します。
func findMatchedMatcher(cmd *parsedCommand, matchers []*Matcher) (*Matcher, []string) {
	for _, m := range matchers {
		if args := m.build(cmd.args); len(args) > 0 {
			return m, args
		}
	}
	return nil, nil
}

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
	Text        string // コマンドからの出力
	IsErrOut    bool
	Spawned     bool
	Finished    bool
	ExitCode    int
}

type Definition struct {
	Timeout        int
	Keyword        string
	Command        string
	Runner         string
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
func ExecutorWithRunner(rq chan *CommandInput, wq chan *CommandOutput, cfgs []*CommandConfig, runnerFactory RunnerFactory) {
	if runnerFactory == nil {
		runnerFactory = func(*CommandConfig) CommandRunner {
			return NewExecRunner()
		}
	}
	// config から matcher を生成
	matchers := make([]*Matcher, 0)
	for _, cfg := range cfgs {
		matcher := newMatcher(cfg)
		if matcher != nil {
			runner := runnerFactory(cfg)
			if runner == nil {
				runner = NewExecRunner()
			}
			matcher.runner = runner
			matchers = append(matchers, matcher)
		}
	}
	// メインループ
	for {
		input, ok := <-rq
		if !ok {
			// The channel has been closed
			return
		}
		msgArr := strings.SplitN(input.Text, "\n", 2)
		cmdMsg := msgArr[0]
		stdinText := ""
		if len(msgArr) >= 2 {
			// メッセージが複数行だった場合、1行目をコマンド、2行目以降を標準入力として扱う
			stdinText = msgArr[1]
		}
		cmds, err := parse(cmdMsg)
		if err != nil && len(cmds) > 0 {
			// parse結果が複数コマンドのときだけエラーを出す
			// 文頭に「>」などのオペレータが来たときにエラーを出されると邪魔なので
			syserr := newErrWriter(wq, input.ReplyInfo, nil)
			fmt.Fprintf(syserr, "%v", err)
			syserr.Flush()
			continue
		}
		// コマンド実行
		ret := 0
		notifiedCommandStart := false
		for _, cmd := range cmds {
			if (ret == 0 && cmd.skipIfSucceeded) || (ret != 0 && cmd.skipIfFailed) {
				continue
			}
			ret = -1
			for _, m := range matchers {
				if args := m.build(cmd.args); len(args) > 0 {
					if !notifiedCommandStart {
						// コマンド実行開始を通知
						wq <- &CommandOutput{
							ReplyInfo: input.ReplyInfo,
							Spawned:   true,
						}
						notifiedCommandStart = true
					}
					var ctx context.Context
					var cancel context.CancelFunc
					if m.cfg.Timeout > 0 {
						ctx, cancel = context.WithTimeout(context.Background(), time.Duration(m.cfg.Timeout)*time.Second)
					} else {
						ctx, cancel = context.WithCancel(context.Background())
					}
					execCmd := m.runner.CommandContext(ctx, args[0], args[1:]...)
					execCmd.SetStdin(strings.NewReader(stdinText))
					stdout := newStdWriter(wq, input.ReplyInfo, m.cfg.ReplyConfig)
					stderr := newErrWriter(wq, input.ReplyInfo, m.cfg.ReplyConfig)
					execCmd.SetStdout(stdout)
					execCmd.SetStderr(stderr)
					ret = execCmd.Run(m.cfg.Timeout)
					stdout.Flush()
					stderr.Flush()
					cancel()
					break
				}
			}
			if ret == -1 {
				// キーワードマッチ失敗
				ret = 128 // command not found
				if len(cmds) > 1 {
					// 複数コマンド実行時のみエラー出力
					syserr := newErrWriter(wq, input.ReplyInfo, nil)
					fmt.Fprintf(syserr, "コマンドが見つかりませんでした: %v", strings.Join(cmd.args, " "))
					syserr.Flush()
				}
			}
		}
		if notifiedCommandStart {
			// コマンド実行終了を通知
			wq <- &CommandOutput{
				ReplyInfo: input.ReplyInfo,
				Finished:  true,
				ExitCode:  ret,
			}
		}
	}
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

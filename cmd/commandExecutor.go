package cmd

import (
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"time"

	"github.com/mattn/go-shellwords"

	"github.com/hnw/slack-commander/pubsub"
)

type CommandConfig struct {
	pubsub.Config
	Keyword string
	Command string
	Aliases []string
}

type commandOption struct {
	args        []string
	stdin       io.Reader
	stdout      io.Writer
	stderr      io.Writer
	cleanupFunc func()
	timeout     int
}

func CommandExecutor(commandQueue chan *pubsub.Input, writeQueue chan *pubsub.CommandOutput, cfgs []*CommandConfig) {
	// config から matcher を生成
	matchers := make([]*commandMatcher, 0)
	for _, cfg := range cfgs {
		matcher := newMatcher(cfg)
		if matcher != nil {
			matchers = append(matchers, matcher)
		}
	}
	// メインループ
	for {
		input, ok := <-commandQueue // closeされると ok が false になる
		if !ok {
			return
		}
		msgArr := strings.SplitN(input.Text, "\n", 2)
		cmdMsg := msgArr[0]
		stdinText := ""
		if len(msgArr) >= 2 {
			// メッセージが複数行だった場合、1行目をコマンド、2行目以降を標準入力として扱う
			stdinText = msgArr[1]
		}
		cmds, err := parseLine(cmdMsg)
		if err != nil && len(cmds) > 0 {
			// parse結果が複数コマンドのときだけエラーを出す
			// 文頭に「>」などのオペレータが来たときにエラーを出されると邪魔なので
			cfg := &CommandConfig{}
			cfg.Username = "Slack commander"
			cfg.IconEmoji = ":ghost:"
			errWriter := getErrorQueueWriter(writeQueue, cfg, input.ReplyInfo)
			errWriter.Write([]byte(fmt.Sprintf("%v", err)))
			errWriter.Flash()
			continue
		}
		ret := 0
		for _, cmd := range cmds {
			if (ret == 0 && cmd.skipIfSucceeded) || (ret != 0 && cmd.skipIfFailed) {
				continue
			}
			ret = -1
			for _, m := range matchers {
				if args := m.build(cmd.args); len(args) > 0 {
					writer := getQueueWriter(writeQueue, m.cfg, input.ReplyInfo)
					errWriter := getErrorQueueWriter(writeQueue, m.cfg, input.ReplyInfo)
					opt := &commandOption{
						args:   args,
						stdin:  strings.NewReader(stdinText),
						stdout: writer,
						stderr: errWriter,
						cleanupFunc: func() {
							writer.Flash()
							errWriter.Flash()
						},
						timeout: m.cfg.Timeout,
					}
					ret = execCommand(opt)
					break
				}
			}
			if ret == -1 && len(cmds) > 1 {
				// 複数コマンド実行時のみ、キーワードマッチ失敗エラーを出す
				cfg := &CommandConfig{}
				cfg.Username = "Slack commander"
				cfg.IconEmoji = ":ghost:"
				errWriter := getErrorQueueWriter(writeQueue, cfg, input.ReplyInfo)
				errWriter.Write([]byte(fmt.Sprintf("コマンドが見つかりませんでした: %v", strings.Join(cmd.args, " "))))
				errWriter.Flash()
			}
		}
	}
}

func getQueueWriter(q chan *pubsub.CommandOutput, cfg *CommandConfig, replyInfo interface{}) *bufferedWriter {
	return newBufferedWriter(func(text string) {
		q <- &pubsub.CommandOutput{
			Config:    cfg.Config,
			ReplyInfo: replyInfo,
			Text:      text,
		}
	})
}

func getErrorQueueWriter(q chan *pubsub.CommandOutput, cfg *CommandConfig, replyInfo interface{}) *bufferedWriter {
	return newBufferedWriter(func(text string) {
		q <- &pubsub.CommandOutput{
			Config:    cfg.Config,
			ReplyInfo: replyInfo,
			Text:      text,
			IsError:   true,
		}
	})
}

// 外部コマンドを実行する
// コマンドを実行できた場合、そのexit codeを返す
// コマンドを実行できかなった場合は127を返す
// 参考：https://tldp.org/LDP/abs/html/exitcodes.html
func execCommand(opt *commandOption) int {
	if len(opt.args) == 0 {
		return 127
	}
	cmd := exec.Command(opt.args[0], opt.args[1:]...)
	cmd.Stdin = opt.stdin
	cmd.Stdout = opt.stdout
	cmd.Stderr = opt.stderr
	if opt.cleanupFunc != nil {
		defer opt.cleanupFunc()
	}

	if err := cmd.Start(); err != nil {
		if opt.stderr != nil {
			opt.stderr.Write([]byte(fmt.Sprintf("%v", err)))
		}
		return 127
	}
	var timer *time.Timer
	if opt.timeout > 0 {
		timer = time.AfterFunc(time.Duration(opt.timeout)*time.Second, func() {
			timer.Stop()
			cmd.Process.Kill()
		})
	}
	err := cmd.Wait()
	if opt.timeout > 0 {
		timer.Stop()
	}
	if err != nil {
		switch err.(type) {
		case *exec.ExitError:
			if exitError, ok := err.(*exec.ExitError); ok {
				if exitError.ExitCode() == -1 {
					// terminated by a signal
					if opt.stderr != nil {
						errText := fmt.Sprintf("timeout %ds exceeded", opt.timeout)
						opt.stderr.Write([]byte(errText))
					}

				}
			}
		default:
			if opt.stderr != nil {
				opt.stderr.Write([]byte(fmt.Sprintf("%v", err)))
			}
		}
	}
	return cmd.ProcessState.ExitCode()
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

func parseLine(line string) ([]*parsedCommand, error) {
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

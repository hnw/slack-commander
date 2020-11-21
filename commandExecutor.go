package main

import (
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/mattn/go-shellwords"
	"github.com/slack-go/slack"
)

func commandExecutor(commandQueue chan *slackInput, writeQueue chan *commandOutput, cfgs []*commandConfig) {
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
		msgArr := strings.SplitN(input.MessageText, "\n", 2)
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
			cfg := &commandConfig{}
			cfg.Username = "Slack commander"
			cfg.IconEmoji = ":ghost:"
			errWriter := getErrorQueueWriter(writeQueue, cfg, input.Message)
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
					writer := getQueueWriter(writeQueue, m.cfg, input.Message)
					errWriter := getErrorQueueWriter(writeQueue, m.cfg, input.Message)
					opt := &commandOption{
						Args:   args,
						Stdin:  strings.NewReader(stdinText),
						Stdout: writer,
						Stderr: errWriter,
						CleanupFunc: func() {
							writer.Flash()
							errWriter.Flash()
						},
						Timeout: m.cfg.Timeout,
					}
					ret = execCommand(opt)
					break
				}
			}
			if ret == -1 && len(cmds) > 1 {
				// 複数コマンド実行時のみ、キーワードマッチ失敗エラーを出す
				cfg := &commandConfig{}
				cfg.Username = "Slack commander"
				cfg.IconEmoji = ":ghost:"
				errWriter := getErrorQueueWriter(writeQueue, cfg, input.Message)
				errWriter.Write([]byte(fmt.Sprintf("コマンドが見つかりませんでした: %v", strings.Join(cmd.args, ""))))
				errWriter.Flash()
			}
		}
	}
}

func getQueueWriter(q chan *commandOutput, cfg *commandConfig, msg *slack.MessageEvent) *bufferedWriter {
	return newBufferedWriter(func(text string) {
		q <- &commandOutput{
			commandConfig: *cfg,
			origMessage:   msg,
			text:          text,
		}
	})
}

func getErrorQueueWriter(q chan *commandOutput, cfg *commandConfig, msg *slack.MessageEvent) *bufferedWriter {
	return newBufferedWriter(func(text string) {
		q <- &commandOutput{
			commandConfig: *cfg,
			origMessage:   msg,
			text:          text,
			isError:       true,
		}
	})
}

// 外部コマンドを実行する
// コマンドを実行できた場合、そのexit codeを返す
// コマンドを実行できかなった場合は127を返す
// 参考：https://tldp.org/LDP/abs/html/exitcodes.html
func execCommand(opt *commandOption) int {
	if len(opt.Args) == 0 {
		return 127
	}
	cmd := exec.Command(opt.Args[0], opt.Args[1:]...)
	cmd.Stdin = opt.Stdin
	cmd.Stdout = opt.Stdout
	cmd.Stderr = opt.Stderr
	if opt.CleanupFunc != nil {
		defer opt.CleanupFunc()
	}

	if err := cmd.Start(); err != nil {
		if opt.Stderr != nil {
			opt.Stderr.Write([]byte(fmt.Sprintf("%v", err)))
		}
		return 127
	}
	var timer *time.Timer
	if opt.Timeout > 0 {
		timer = time.AfterFunc(time.Duration(opt.Timeout)*time.Second, func() {
			timer.Stop()
			cmd.Process.Kill()
		})
	}
	err := cmd.Wait()
	if opt.Timeout > 0 {
		timer.Stop()
	}
	if err != nil {
		switch err.(type) {
		case *exec.ExitError:
			if exitError, ok := err.(*exec.ExitError); ok {
				if exitError.ExitCode() == -1 {
					// terminated by a signal
					if opt.Stderr != nil {
						errText := fmt.Sprintf("timeout %ds exceeded", opt.Timeout)
						opt.Stderr.Write([]byte(errText))
					}

				}
			}
		default:
			if opt.Stderr != nil {
				opt.Stderr.Write([]byte(fmt.Sprintf("%v", err)))
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

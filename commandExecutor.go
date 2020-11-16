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
		err := checkSyntax(cmdMsg)
		if err != nil {
			cfg := &commandConfig{}
			cfg.Username = "Slack commander"
			cfg.IconEmoji = ":ghost:"
			errWriter := getErrorQueueWriter(writeQueue, cfg, input.Message)
			errWriter.Write([]byte(fmt.Sprintf("%v", err)))
			errWriter.Flash()
			continue
		}
		for _, matcher := range matchers {
			if args, err := matcher.replace(cmdMsg); err == nil {
				writer := getQueueWriter(writeQueue, &matcher.commandConfig, input.Message)
				errWriter := getErrorQueueWriter(writeQueue, &matcher.commandConfig, input.Message)
				opt := &commandOption{
					Args:   args,
					Stdin:  strings.NewReader(stdinText),
					Stdout: writer,
					Stderr: errWriter,
					CleanupFunc: func() {
						writer.Flash()
						errWriter.Flash()
					},
					Timeout: matcher.commandConfig.Timeout,
				}
				execCommand(opt)
				break
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

func execCommand(opt *commandOption) int {
	if len(opt.Args) == 0 {
		return -1
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
		return -1
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

func checkSyntax(line string) error {
	parser := shellwords.NewParser()
	prevSeparator := ""
	for {
		args, err := parser.Parse(line)
		//fmt.Printf("args=%v\n", args)
		if len(args) == 0 {
			if prevSeparator != "" {
				return errors.New("Parse error near `" + prevSeparator + "'")
			}
			end := 2
			if len(line) < 2 {
				end = len(line)
			}
			return errors.New("Parse error near `" + string([]rune(line)[0:end]) + "'")
		}
		if err != nil {
			return err
		}
		if parser.Position < 0 {
			return nil
		}
		i := parser.Position
		//fmt.Printf("i=%v\n", i)
		token := line[i:]
		separators := []string{";", "&&", "||"}
		prevSeparator = ""
		for _, sep := range separators {
			if strings.HasPrefix(token, sep) {
				i += len(sep)
				prevSeparator = sep
				break
			}
		}
		if prevSeparator == "" {
			end := 2
			if len(token) < 2 {
				end = len(token)
			}
			return errors.New("Parse error near `" + string([]rune(token)[0:end]) + "'")
		}
		line = string(line[i:])
	}
}

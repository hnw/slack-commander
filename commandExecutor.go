package main

import (
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/mattn/go-shellwords"
)

func commandExecutor(commandQueue chan *commandInfo, writeQueue chan *commandInfo) {
	for {
		info, ok := <-commandQueue // closeされると ok が false になる
		if !ok {
			return
		}
		execCommand(info, writeQueue)
	}
}

func execCommand(info *commandInfo, writeQueue chan *commandInfo) int {
	msgArr := strings.SplitN(info.MessageText, "\n", 2)
	cmdText := msgArr[0]
	inputText := ""
	if len(msgArr) >= 2 {
		// メッセージが複数行だった場合、1行目をコマンド、2行目以降を標準入力として扱う
		inputText = msgArr[1]
	}
	cmdConfig, wildcard := matcher.matchedCommand(cmdText)
	if cmdConfig == nil {
		// Command not found相当
		return 127
	}
	args, err := cmdConfig.buildCommandParams(wildcard)
	if err != nil {
		// コマンドの組み立てに失敗
		fmt.Printf("%v\n", err)
		return 1
	}

	cmd := exec.Command(args[0], args[1:]...)

	stdInfo := *info
	stdInfo.Config = cmdConfig
	stdoutWriter := stdInfo.getQueueWriter(writeQueue)
	cmd.Stdin = strings.NewReader(inputText)
	cmd.Stdout = stdoutWriter
	defer stdoutWriter.Flash()

	errInfo := *info
	errInfo.Config = cmdConfig
	errInfo.ErrorOccurred = true
	stderrWriter := errInfo.getQueueWriter(writeQueue)
	cmd.Stderr = stderrWriter
	defer stderrWriter.Flash()

	if err := cmd.Start(); err != nil {
		stderrWriter.Write([]byte(fmt.Sprintf("%v", err)))
		return -1
	}
	var timer *time.Timer
	if cmdConfig.Timeout > 0 {
		timer = time.AfterFunc(time.Duration(cmdConfig.Timeout)*time.Second, func() {
			timer.Stop()
			cmd.Process.Kill()
		})
	}
	err = cmd.Wait()
	if cmdConfig.Timeout > 0 {
		timer.Stop()
	}
	if err != nil {
		switch err.(type) {
		case *exec.ExitError:
			if exitError, ok := err.(*exec.ExitError); ok {
				if exitError.ExitCode() == -1 {
					// terminated by a signal
					errText := fmt.Sprintf("timeout %ds exceeded", cmdConfig.Timeout)
					stderrWriter.Write([]byte(errText))
				}
			}
		default:
			stderrWriter.Write([]byte(fmt.Sprintf("%v", err)))
		}
	}
	return cmd.ProcessState.ExitCode()
}

func (m *commandMatcher) matchedCommand(msg string) (*commandConfig, string) {
	for i, re := range m.Regexps {
		matches := re.FindAllStringSubmatch(msg, 1)
		if matches != nil {
			submatched := ""
			if len(matches[0]) >= 2 {
				submatched = matches[0][1]
			}
			return m.Commands[i], submatched
		}
	}
	return nil, ""
}

func (c *commandConfig) buildCommandParams(wildcard string) ([]string, error) {
	p := shellwords.NewParser()
	args, err := p.Parse(c.Command)
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
				if strings.Index(c.Command, `'*'`) != -1 {
					// '*' なら何もしない
					newArgs = args
					break
				}
				newArgs = append(newArgs, args[:i]...)
				if strings.Index(c.Command, `"*"`) != -1 {
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

func (info *commandInfo) getQueueWriter(writeQueue chan *commandInfo) *slackBuffer {
	b := slackBuffer{}
	b.queue = &writeQueue
	b.info = info
	return &b
}

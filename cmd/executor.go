package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"strings"
	"time"

	"github.com/mattn/go-shellwords"
)

// CommandInput はPubSubからの情報をExecutorに引き渡す構造体
type CommandInput struct {
	ConversationID        ConversationID
	MessageID             MessageID
	Text                  string // 起動コマンド平文
	ResolvedInput         *ResolvedInput
	AllowedCommandIndexes []int
}

// ConversationID identifies the Slack thread that receives command output.
type ConversationID struct {
	ChannelID     string
	RootTimestamp string
}

// MessageID identifies the Slack message that triggered command execution.
type MessageID struct {
	ChannelID string
	Timestamp string
}

// CommandOutput carries execution output and input errors through the output queue.
type CommandOutput struct {
	ReplyConfig    interface{}
	ConversationID ConversationID
	MessageID      MessageID
	Text           string // コマンドからのテキスト出力（ImageData と排他）
	ImageData      []byte // sixel を変換した PNG バイト列（Text と排他）
	IsErrOut       bool
	Spawned        bool
	Finished       bool
	ExitCode       int
}

// RunnerFactory returns a runner for the given execution definition.
type RunnerFactory func(config RunnerConfig) CommandRunner

// Executor executes individual command inputs.
type Executor struct {
	outputQueue chan *CommandOutput
}

// NewExecutor creates an executor for prepared command inputs.
func NewExecutor(outputQueue chan *CommandOutput) *Executor {
	return &Executor{outputQueue: outputQueue}
}

// Execute processes one command input without queue or conversation ownership.
func (e *Executor) Execute(
	ctx context.Context,
	input *CommandInput,
	lifecycle StdinLifecycle,
) {
	if ctx == nil {
		ctx = context.Background()
	}
	parsed := input.ResolvedInput
	if parsed == nil {
		return
	}
	if parsed.ParseErr != nil {
		return
	}
	cmds, stdinText := parsed.Commands, parsed.StdinText

	rawBody := ""
	initialStdin := stdinText
	inputLifecycle := lifecycle

	if len(cmds) == 0 {
		return
	}
	if len(cmds) > 1 && !allCommandsAllowedInChain(cmds) {
		return
	}
	command := cmds[0].Command
	if command != nil && command.config.InputBodyMode == InputBodyArgument {
		if stdinText != "" {
			rawBody = "\n" + stdinText
		}
		initialStdin = ""
	}

	if command == nil || !command.config.InteractiveStdin {
		inputLifecycle = nil
	}

	_ = executeCommands(
		ctx,
		cmds,
		initialStdin,
		rawBody,
		input,
		e.outputQueue,
		inputLifecycle,
	)
}

func allCommandsAllowedInChain(commands []ResolvedCommand) bool {
	for _, resolved := range commands {
		if resolved.Command == nil {
			continue
		}
		if !resolved.Command.config.AllowInChain {
			return false
		}
	}
	return true
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

func executeCommands(
	ctx context.Context,
	cmds []ResolvedCommand,
	stdinText string,
	rawBody string,
	input *CommandInput,
	wq chan *CommandOutput,
	lifecycle StdinLifecycle,
) int {
	ret := 0
	for i, resolved := range cmds {
		if shouldSkipCommand(resolved.Part, ret) {
			continue
		}
		ret = -1
		command, args := resolved.Command, resolved.Args
		if command == nil {
			ret = writeCommandNotFound(wq, input, resolved.Part)
			continue
		}
		if i == 0 {
			// コマンド実行開始を通知
			wq <- &CommandOutput{
				ConversationID: input.ConversationID,
				MessageID:      input.MessageID,
				Spawned:        true,
			}
			// 関数を抜ける時に必ず終了通知を送る
			defer func() {
				wq <- &CommandOutput{
					ConversationID: input.ConversationID,
					MessageID:      input.MessageID,
					Finished:       true,
					ExitCode:       ret,
				}
			}()
		}
		if rawBody != "" && command.hasTrailingWildcard() {
			args = append(args, rawBody)
		}
		ret = runMatchedCommand(ctx, command, args, stdinText, input, wq, lifecycle)
	}
	return ret
}

func shouldSkipCommand(cmd *commandPart, ret int) bool {
	if ret == 0 && cmd.skipIfSucceeded {
		return true
	}
	return ret != 0 && cmd.skipIfFailed
}

func writeCommandNotFound(wq chan *CommandOutput, input *CommandInput, cmd *commandPart) int {
	syserr := newErrWriter(wq, nil, input.ConversationID, input.MessageID, DefaultOutputFlushInterval)
	_, _ = fmt.Fprintf(syserr, "コマンドが見つかりませんでした: %v", strings.Join(cmd.args, " "))
	_ = syserr.Flush()
	return 127
}

func runMatchedCommand(
	ctx context.Context,
	command *Command,
	args []string,
	stdinText string,
	input *CommandInput,
	wq chan *CommandOutput,
	lifecycle StdinLifecycle,
) int {
	var cmdCtx context.Context
	var cancel context.CancelFunc
	if command.config.Timeout > 0 {
		cmdCtx, cancel = context.WithTimeout(
			ctx,
			time.Duration(command.config.Timeout)*time.Second,
		)
	} else {
		cmdCtx, cancel = context.WithCancel(ctx)
	}
	defer cancel()

	execCmd := command.runner.CommandContext(cmdCtx, args[0], args[1:]...)
	setSlackContextEnvironment(execCmd, input.ConversationID)
	stdout := newStdWriter(wq, command.config.ReplyConfig, input.ConversationID, input.MessageID, command.config.OutputFlushInterval)
	stderr := newErrWriter(wq, command.config.ReplyConfig, input.ConversationID, input.MessageID, command.config.OutputFlushInterval)
	if command.config.TTY {
		terminal := newTTYOutputNormalizer(stdout)
		if cmd, ok := execCmd.(interface{ SetTTY() }); ok {
			cmd.SetTTY()
		}
		execCmd.SetStdout(terminal)
		execCmd.SetStderr(terminal)
		ret := runWithLifecycleInputWithLineEnding(
			execCmd,
			command.config.Timeout,
			0,
			stdinText,
			input.ConversationID,
			lifecycle,
			"\r",
		)
		_ = terminal.Flush()
		return ret
	}
	execCmd.SetStdout(stdout)
	execCmd.SetStderr(stderr)
	ret := runWithLifecycleInput(execCmd, command.config.Timeout, time.Duration(command.config.StdinIdleTimeout)*time.Second, stdinText, input.ConversationID, lifecycle)
	_ = stdout.Flush()
	_ = stderr.Flush()

	return ret
}

func runWithLifecycleInput(
	command Cmd,
	timeout int,
	idle time.Duration,
	initial string,
	conversation ConversationID,
	lifecycle StdinLifecycle,
) int {
	return runWithLifecycleInputWithLineEnding(command, timeout, idle, initial, conversation, lifecycle, "\n")
}

func runWithLifecycleInputWithLineEnding(
	command Cmd,
	timeout int,
	idle time.Duration,
	initial string,
	conversation ConversationID,
	lifecycle StdinLifecycle,
	lineEnding string,
) int {
	runner, ok := command.(interface {
		RunWithStdin(int, func(io.WriteCloser)) int
	})
	if !ok {
		command.SetStdin(strings.NewReader(initial))
		return command.Run(timeout)
	}
	if lifecycle == nil {
		session := newStdinSession(initial, nil)
		defer session.Close()
		return runner.RunWithStdin(timeout, session.Start)
	}
	onError := func(err error) {
		log.Printf("[WARN] live stdin write failed channel=%s thread=%s: %v", conversation.ChannelID, conversation.RootTimestamp, err)
	}
	endpoint := newInteractiveStdinSessionWithLineEnding(initial, idle, onError, lineEnding)
	endpoint.onClose = func() { lifecycle.StdinClosed(endpoint) }
	defer endpoint.Close()
	return runner.RunWithStdin(timeout, func(stdin io.WriteCloser) {
		endpoint.Start(stdin)
		lifecycle.StdinReady(endpoint)
	})
}

type commandPart struct {
	skipIfSucceeded bool
	skipIfFailed    bool
	args            []string
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
		cmds = append(cmds, newCommandPart(prevOperator, args))
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

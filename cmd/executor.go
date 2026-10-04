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
	stdinTarget           *InteractiveStdin
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

// RunnerFactory returns a runner for the given execution definition.
type RunnerFactory func(config RunnerConfig) CommandRunner

// Executor executes individual command inputs.
type Executor struct{}

// NewExecutor creates an executor for prepared command inputs.
func NewExecutor() *Executor {
	return &Executor{}
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

	if len(cmds) == 0 || cmds[0].Command == nil {
		return
	}
	command := cmds[0].Command
	if command != nil && command.config.InputBodyMode == InputBodyArgument {
		if stdinText != "" {
			rawBody = "\n" + stdinText
		}
		initialStdin = ""
	}

	_ = executeCommands(
		ctx,
		cmds,
		initialStdin,
		rawBody,
		input,
		lifecycle,
	)
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
			ret = writeCommandNotFound(cmds[0].Command, input, resolved.Part)
			continue
		}
		if i == 0 {
			command.output.Start(input.ConversationID, input.MessageID)
			defer func() {
				command.output.Finish(input.ConversationID, input.MessageID, ret)
			}()
		}
		if rawBody != "" && command.hasTrailingWildcard() {
			args = append(args, rawBody)
		}
		commandLifecycle := lifecycle
		if !command.config.InteractiveStdin {
			commandLifecycle = nil
		}
		ret = runMatchedCommand(ctx, command, args, stdinText, input, commandLifecycle)
	}
	return ret
}

func shouldSkipCommand(cmd *commandPart, ret int) bool {
	if ret == 0 && cmd.skipIfSucceeded {
		return true
	}
	return ret != 0 && cmd.skipIfFailed
}

func writeCommandNotFound(owner *Command, input *CommandInput, cmd *commandPart) int {
	owner.output.SystemError(input.ConversationID, input.MessageID, fmt.Sprintf("コマンドが見つかりませんでした: %v", strings.Join(cmd.args, " ")), SystemErrorCommandNotFound)
	return 127
}

var errCommandTimeout = errors.New("command timeout")

func runMatchedCommand(
	ctx context.Context,
	command *Command,
	args []string,
	stdinText string,
	input *CommandInput,
	lifecycle StdinLifecycle,
) int {
	var cmdCtx context.Context
	var cancel context.CancelFunc
	if command.config.Timeout > 0 {
		cmdCtx, cancel = context.WithTimeoutCause(
			ctx,
			command.config.Timeout,
			errCommandTimeout,
		)
	} else {
		cmdCtx, cancel = context.WithCancel(ctx)
	}
	defer cancel()

	execCmd := command.runner.CommandContext(cmdCtx, args[0], args[1:]...)
	setSlackContextEnvironment(execCmd, input.ConversationID)
	stdout := command.output.Stdout(input.ConversationID, input.MessageID)
	stderr := command.output.Stderr(input.ConversationID, input.MessageID)
	if command.config.TTY {
		terminal := newTTYOutputNormalizer(stdout)
		if cmd, ok := execCmd.(interface{ SetTTY() }); ok {
			cmd.SetTTY()
		}
		execCmd.SetStdout(terminal)
		execCmd.SetStderr(terminal)
		ret := runWithLifecycleInputWithLineEnding(
			execCmd,
			0,
			stdinText,
			input.ConversationID,
			lifecycle,
			implicitStdinReplyCommand(command),
			"\r",
		)
		cancel()
		if errors.Is(context.Cause(cmdCtx), errCommandTimeout) {
			_, _ = fmt.Fprintf(terminal, "Timeout exceeded (%s)", command.config.Timeout)
		}
		_ = terminal.Flush()
		return ret
	}
	execCmd.SetStdout(stdout)
	execCmd.SetStderr(stderr)
	ret := runWithLifecycleInput(execCmd, command.config.StdinIdleTimeout, stdinText, input.ConversationID, lifecycle, implicitStdinReplyCommand(command))
	cancel()
	if errors.Is(context.Cause(cmdCtx), errCommandTimeout) {
		_, _ = fmt.Fprintf(stderr, "Timeout exceeded (%s)", command.config.Timeout)
	}
	_ = stdout.Flush()
	_ = stderr.Flush()

	return ret
}

func runWithLifecycleInput(
	command Cmd,
	idle time.Duration,
	initial string,
	conversation ConversationID,
	lifecycle StdinLifecycle,
	implicitReplyCommand *Command,
) int {
	return runWithLifecycleInputWithLineEnding(command, idle, initial, conversation, lifecycle, implicitReplyCommand, "\n")
}

func runWithLifecycleInputWithLineEnding(
	command Cmd,
	idle time.Duration,
	initial string,
	conversation ConversationID,
	lifecycle StdinLifecycle,
	implicitReplyCommand *Command,
	lineEnding string,
) int {
	runner, ok := command.(interface {
		RunWithStdin(func(io.WriteCloser)) int
	})
	if !ok {
		command.SetStdin(strings.NewReader(initial))
		return command.Run()
	}
	if lifecycle == nil {
		session := newStdinSession(initial, nil)
		defer session.Close()
		return runner.RunWithStdin(session.Start)
	}
	onError := func(err error) {
		log.Printf("[WARN] live stdin write failed channel=%s thread=%s: %v", conversation.ChannelID, conversation.RootTimestamp, err)
	}
	endpoint := newInteractiveStdinSessionWithLineEnding(initial, idle, onError, lineEnding)
	endpoint.onClose = func() { lifecycle.StdinClosed(endpoint) }
	defer endpoint.Close()
	return runner.RunWithStdin(func(stdin io.WriteCloser) {
		endpoint.Start(stdin)
		lifecycle.StdinReady(endpoint, implicitReplyCommand)
	})
}

func implicitStdinReplyCommand(command *Command) *Command {
	if command.replies == nil || len(command.replies.commands) == 0 {
		return nil
	}
	return command.replies.commands[0]
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

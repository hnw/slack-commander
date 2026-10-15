package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"strings"
	"time"
)

// CommandInput はPubSubからの情報をExecutorに引き渡す構造体
type CommandInput struct {
	ConversationID        ConversationID
	MessageID             MessageID
	Text                  string // 起動コマンド平文
	Commands              []ResolvedCommand
	ParseErr              error
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
	if input.ParseErr != nil || len(input.Commands) == 0 || input.Commands[0].Command == nil {
		return
	}
	_ = executeCommands(ctx, input.Commands, input, lifecycle)
}

func executeCommands(
	ctx context.Context,
	cmds []ResolvedCommand,
	input *CommandInput,
	lifecycle StdinLifecycle,
) int {
	ret := 0
	for i, resolved := range cmds {
		if shouldSkipCommand(resolved, ret) {
			continue
		}
		command := resolved.Command
		if resolved.Err != nil {
			cmds[0].Command.output.SystemError(input.ConversationID, input.MessageID, resolved.Err.Error(), SystemErrorCommandNotFound)
			ret = 127
			continue
		}
		if i == 0 {
			command.output.Start(input.ConversationID, input.MessageID)
			defer func() {
				command.output.Finish(input.ConversationID, input.MessageID, ret)
			}()
		}
		commandLifecycle := lifecycle
		if !command.config.InteractiveStdin {
			commandLifecycle = nil
		}
		ret = runMatchedCommand(ctx, resolved, input, commandLifecycle)
	}
	return ret
}

func shouldSkipCommand(cmd ResolvedCommand, ret int) bool {
	if ret == 0 && cmd.skipIfSucceeded {
		return true
	}
	return ret != 0 && cmd.skipIfFailed
}

var errCommandTimeout = errors.New("command timeout")

func runMatchedCommand(
	ctx context.Context,
	resolved ResolvedCommand,
	input *CommandInput,
	lifecycle StdinLifecycle,
) int {
	command, stdinText := resolved.Command, resolved.StdinText
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

	execCmd := command.runner.CommandContext(cmdCtx, resolved.Wildcard, resolved.HasWildcard)
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

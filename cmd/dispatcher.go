package cmd

import (
	"context"
	"strings"
)

// DispatchResult はcommandのdispatch結果を表す。
type DispatchResult int

const (
	// DispatchIgnored は入力を実行先へ渡さなかったことを表す。
	DispatchIgnored DispatchResult = iota
	// DispatchAccepted は入力が実行先へ渡されたことを表す。
	DispatchAccepted
	// DispatchQueueFull はqueueが満杯で入力を受け付けなかったことを表す。
	DispatchQueueFull
)

// CommandDispatcher はmatch済みcommandの実行先を選ぶ。
type CommandDispatcher struct {
	enqueue func(*CommandInput) bool
}

// NewCommandDispatcher はqueued commandをenqueueへ渡すdispatcherを作る。
func NewCommandDispatcher(enqueue func(*CommandInput) bool) *CommandDispatcher {
	return &CommandDispatcher{enqueue: enqueue}
}

// DispatchRoot はroot inputをcommand queueへ渡す。
func (d *CommandDispatcher) DispatchRoot(input *CommandInput) DispatchResult {
	if d.enqueue == nil || !d.enqueue(input) {
		return DispatchQueueFull
	}
	return DispatchAccepted
}

// DispatchReply はdirect replyを実行するか、queued replyをcommand queueへ渡す。
func (d *CommandDispatcher) DispatchReply(command *Command, args []string, commands *CommandSet, input *CommandInput) DispatchResult {
	if command.config.DispatchPolicy == DispatchDirect {
		runnerArgs := append(args[1:], input.ConversationID.ChannelID, input.ConversationID.RootTimestamp)
		directCmd := command.runner.CommandContext(context.Background(), args[0], runnerArgs...)
		directCmd.SetStdin(strings.NewReader(input.Text))
		if directCmd.Run(0) != 0 {
			return DispatchIgnored
		}
		return DispatchAccepted
	}
	if d.enqueue == nil {
		return DispatchIgnored
	}
	input.CommandSet = commands
	if !d.enqueue(input) {
		return DispatchQueueFull
	}
	return DispatchAccepted
}

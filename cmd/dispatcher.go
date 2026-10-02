package cmd

import (
	"context"
	"strings"
	"sync"
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
	mu                sync.Mutex
	closed            bool
	ctx               context.Context
	executor          *Executor
	stdinStore        *StdinStore
	conversationLocks *ConversationLocks
	enqueue           func(*CommandInput) bool
	asyncWG           sync.WaitGroup
}

// NewCommandDispatcher はcommandのqueueと非同期実行を管理する。
func NewCommandDispatcher(
	ctx context.Context,
	executor *Executor,
	stdinStore *StdinStore,
	conversationLocks *ConversationLocks,
	enqueue func(*CommandInput) bool,
) *CommandDispatcher {
	return &CommandDispatcher{
		ctx:               ctx,
		executor:          executor,
		stdinStore:        stdinStore,
		conversationLocks: conversationLocks,
		enqueue:           enqueue,
	}
}

// DispatchRoot はroot inputをqueueまたはHTTP実行へ渡す。
func (d *CommandDispatcher) DispatchRoot(command *Command, input *CommandInput) DispatchResult {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return DispatchIgnored
	}
	if isHTTPCommand(command) {
		d.startAsyncLocked(input)
		return DispatchAccepted
	}
	if d.enqueue == nil || !d.enqueue(input) {
		return DispatchQueueFull
	}
	return DispatchAccepted
}

// DispatchReply はdirect replyを実行するか、queued replyをqueueまたはHTTP実行へ渡す。
func (d *CommandDispatcher) DispatchReply(command *Command, args []string, commands *CommandSet, input *CommandInput) DispatchResult {
	if command.config.DispatchPolicy == DispatchDirect {
		d.mu.Lock()
		closed := d.closed
		d.mu.Unlock()
		if closed {
			return DispatchIgnored
		}
		return runDirectReply(command, args, input)
	}
	input.CommandSet = commands
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return DispatchIgnored
	}
	if isHTTPCommand(command) && commands.MatchSingle(input.Text, input.AllowedCommandIndexes) == command {
		d.startAsyncLocked(input)
		return DispatchAccepted
	}
	if d.enqueue == nil {
		return DispatchIgnored
	}
	if !d.enqueue(input) {
		return DispatchQueueFull
	}
	return DispatchAccepted
}

// Close は以後のdispatchを拒否し、queueへの新しい送信も止める。
func (d *CommandDispatcher) Close() {
	d.mu.Lock()
	d.closed = true
	d.mu.Unlock()
}

// Wait はClose後に開始済みのHTTP実行が終わるまで待つ。
func (d *CommandDispatcher) Wait() {
	d.asyncWG.Wait()
}

func (d *CommandDispatcher) startAsyncLocked(input *CommandInput) {
	d.asyncWG.Add(1)
	go func() {
		defer d.asyncWG.Done()
		unlock := d.conversationLocks.Lock(input.ConversationID)
		defer unlock()
		d.executor.Execute(d.ctx, input, d.stdinStore.Lifecycle(input.ConversationID))
	}()
}

func isHTTPCommand(command *Command) bool {
	return command != nil && command.config.Runner == RunnerHTTP
}

func runDirectReply(command *Command, args []string, input *CommandInput) DispatchResult {
	runnerArgs := append(args[1:], input.ConversationID.ChannelID, input.ConversationID.RootTimestamp)
	directCmd := command.runner.CommandContext(context.Background(), args[0], runnerArgs...)
	directCmd.SetStdin(strings.NewReader(input.Text))
	if directCmd.Run(0) != 0 {
		return DispatchIgnored
	}
	return DispatchAccepted
}

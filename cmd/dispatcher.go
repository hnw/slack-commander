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

// Dispatch routes a prepared input to direct execution, asynchronous execution, or the worker queue.
func (d *CommandDispatcher) Dispatch(input *CommandInput) DispatchResult {
	d.mu.Lock()
	if d.closed {
		d.mu.Unlock()
		return DispatchIgnored
	}
	parsed := input.ResolvedInput
	if parsed == nil {
		d.mu.Unlock()
		return DispatchIgnored
	}
	single := parsed.SingleCommand()
	if single == nil {
		defer d.mu.Unlock()
		if d.enqueue == nil || !d.enqueue(input) {
			return DispatchQueueFull
		}
		return DispatchAccepted
	}
	if single.config.DispatchPolicy == DispatchDirect {
		d.mu.Unlock()
		return runDirectCommand(single, parsed.Commands[0].Args, input)
	}
	defer d.mu.Unlock()
	if isHTTPCommand(single) {
		d.startAsyncLocked(input)
		return DispatchAccepted
	}
	if d.enqueue == nil || !d.enqueue(input) {
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

func runDirectCommand(command *Command, args []string, input *CommandInput) DispatchResult {
	runnerArgs := args[1:]
	if command.config.Runner == RunnerStdinReply {
		runnerArgs = append(runnerArgs, input.ConversationID.ChannelID, input.ConversationID.RootTimestamp)
	}
	directCmd := command.runner.CommandContext(context.Background(), args[0], runnerArgs...)
	directCmd.SetStdin(strings.NewReader(input.Text))
	if directCmd.Run(0) != 0 {
		return DispatchIgnored
	}
	return DispatchAccepted
}

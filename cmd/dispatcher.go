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
	outputQueue       chan *CommandOutput
	stdinStore        *StdinStore
	conversationLocks *ConversationLocks
	enqueue           func(*CommandInput) bool
	asyncWG           sync.WaitGroup
}

// NewCommandDispatcher はcommandのqueueと非同期実行を管理する。
func NewCommandDispatcher(
	ctx context.Context,
	executor *Executor,
	outputQueue chan *CommandOutput,
	stdinStore *StdinStore,
	conversationLocks *ConversationLocks,
	enqueue func(*CommandInput) bool,
) *CommandDispatcher {
	return &CommandDispatcher{
		ctx:               ctx,
		executor:          executor,
		outputQueue:       outputQueue,
		stdinStore:        stdinStore,
		conversationLocks: conversationLocks,
		enqueue:           enqueue,
	}
}

// Dispatch routes a resolved input according to its dispatch target.
func (d *CommandDispatcher) Dispatch(input *CommandInput) DispatchResult {
	d.mu.Lock()
	if d.closed || input == nil {
		d.mu.Unlock()
		return DispatchIgnored
	}
	parsed := input.ResolvedInput
	target, ok, parseErr := dispatchPlan(parsed)
	if !ok {
		d.mu.Unlock()
		return DispatchIgnored
	}
	if parseErr {
		output := &CommandOutput{
			ReplyConfig:    parsed.Commands[0].Command.config.SystemReplyConfig,
			ConversationID: input.ConversationID,
			MessageID:      input.MessageID,
			Text:           parsed.ParseErr.Error(),
			IsErrOut:       true,
			ExitCode:       2,
		}
		d.asyncWG.Add(1)
		go func() {
			defer d.asyncWG.Done()
			d.outputQueue <- output
		}()
		d.mu.Unlock()
		return DispatchAccepted
	}
	switch target {
	case DispatchRunner:
		command, args := parsed.Commands[0].Command, parsed.Commands[0].Args
		d.mu.Unlock()
		return runDirectCommand(command, args, input)
	case DispatchExecutor:
		d.startExecutorLocked(input)
		d.mu.Unlock()
		return DispatchAccepted
	case DispatchQueue:
		if d.enqueue == nil || !d.enqueue(input) {
			d.mu.Unlock()
			return DispatchQueueFull
		}
		d.mu.Unlock()
		return DispatchAccepted
	default:
		d.mu.Unlock()
		return DispatchIgnored
	}
}

func dispatchPlan(parsed *ResolvedInput) (target DispatchMode, accepted bool, parseError bool) {
	if parsed == nil || len(parsed.Commands) == 0 || parsed.Commands[0].Command == nil {
		return 0, false, false
	}
	if parsed.ParseErr != nil {
		return 0, true, true
	}
	if len(parsed.Commands) > 1 && !allCommandsAllowedInChain(parsed.Commands) {
		return 0, false, false
	}
	target, ok := parsed.DispatchTarget()
	return target, ok, false
}

func resolvedInputExecutable(parsed *ResolvedInput) bool {
	_, ok, parseErr := dispatchPlan(parsed)
	return ok && !parseErr
}

func allCommandsAllowedInChain(commands []ResolvedCommand) bool {
	for _, resolved := range commands {
		if resolved.Command != nil && !resolved.Command.config.AllowInChain {
			return false
		}
	}
	return true
}

// Close prevents subsequent dispatches from accepting new work.
func (d *CommandDispatcher) Close() {
	d.mu.Lock()
	d.closed = true
	d.mu.Unlock()
}

// Wait blocks until all asynchronous work accepted before Close has completed.
func (d *CommandDispatcher) Wait() {
	d.asyncWG.Wait()
}

func (d *CommandDispatcher) startExecutorLocked(input *CommandInput) {
	d.asyncWG.Add(1)
	go func() {
		defer d.asyncWG.Done()
		unlock := d.conversationLocks.Lock(input.ConversationID)
		defer unlock()
		d.executor.Execute(d.ctx, input, d.stdinStore.Lifecycle(input.ConversationID))
	}()
}

func runDirectCommand(command *Command, args []string, input *CommandInput) DispatchResult {
	runnerArgs := args[1:]
	directCmd := command.runner.CommandContext(context.Background(), args[0], runnerArgs...)
	if command.config.Runner == RunnerStdinReply {
		setter, ok := directCmd.(interface{ SetStdinTarget(*InteractiveStdin) })
		if !ok || input.stdinTarget == nil {
			return DispatchIgnored
		}
		setter.SetStdinTarget(input.stdinTarget)
	}
	directCmd.SetStdin(strings.NewReader(input.Text))
	if directCmd.Run(0) != 0 {
		return DispatchIgnored
	}
	return DispatchAccepted
}

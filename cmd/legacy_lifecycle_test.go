package cmd

import (
	"context"
	"sync"
	"time"
)

type testThreadRegistry struct {
	mu     sync.Mutex
	inputs map[ConversationID]*InteractiveStdin
}

func (r *testThreadRegistry) register(conversation ConversationID, input *InteractiveStdin) {
	input.mu.Lock()
	defer input.mu.Unlock()
	if input.closed {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.inputs == nil {
		r.inputs = make(map[ConversationID]*InteractiveStdin)
	}
	r.inputs[conversation] = input
}

func (r *testThreadRegistry) lookup(conversation ConversationID) *InteractiveStdin {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.inputs[conversation]
}

func (r *testThreadRegistry) unregister(conversation ConversationID, input *InteractiveStdin) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.inputs[conversation] == input {
		delete(r.inputs, conversation)
	}
}

type testLifecycle struct {
	registry     *testThreadRegistry
	conversation ConversationID
}

func (l testLifecycle) StdinReady(input *InteractiveStdin) {
	l.registry.register(l.conversation, input)
}

func (l testLifecycle) StdinClosed(input *InteractiveStdin) {
	l.registry.unregister(l.conversation, input)
}

func testRunWithInput(command Cmd, timeout int, idle time.Duration, initial string, conversation ConversationID, registry *testThreadRegistry) int {
	if registry == nil || conversation.ChannelID == "" || conversation.RootTimestamp == "" {
		return runWithLifecycleInput(command, timeout, idle, initial, conversation, nil)
	}
	return runWithLifecycleInput(command, timeout, idle, initial, conversation, testLifecycle{registry: registry, conversation: conversation})
}

func testExecutorWithLifecycle(ctx context.Context, rq chan *CommandInput, wq chan *CommandOutput, cfgs []*testCommandConfig, runnerFactory func(*testExecutionConfig) CommandRunner, registry *testThreadRegistry) {
	executor := NewExecutor(testCommandSet(cfgs, runnerFactory), wq)
	for input := range rq {
		var lifecycle StdinLifecycle
		if registry != nil {
			lifecycle = testLifecycle{
				registry:     registry,
				conversation: input.ConversationID,
			}
		}
		executor.Execute(ctx, input, lifecycle)
	}
}

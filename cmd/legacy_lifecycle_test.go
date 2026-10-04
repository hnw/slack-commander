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

func (l testLifecycle) StdinReady(input *InteractiveStdin, _ *Command) {
	l.registry.register(l.conversation, input)
}

func (l testLifecycle) StdinClosed(input *InteractiveStdin) {
	l.registry.unregister(l.conversation, input)
}

func testRunWithInput(command Cmd, idle time.Duration, initial string, conversation ConversationID, registry *testThreadRegistry) int {
	if registry == nil || conversation.ChannelID == "" || conversation.RootTimestamp == "" {
		return runWithLifecycleInput(command, idle, initial, conversation, nil, nil)
	}
	return runWithLifecycleInput(command, idle, initial, conversation, testLifecycle{registry: registry, conversation: conversation}, nil)
}

func testExecutorWithLifecycle(ctx context.Context, rq chan *CommandInput, wq chan *CommandOutput, registry *testThreadRegistry) {
	executor := NewExecutor()
	for input := range rq {
		for _, resolved := range input.ResolvedInput.Commands {
			NewCommandSet([]*Command{resolved.Command}).ConfigureOutput(wq)
		}
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

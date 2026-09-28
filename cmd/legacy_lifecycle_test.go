package cmd

import (
	"context"
	"sync"
	"time"
)

type testThreadRegistry struct {
	mu     sync.Mutex
	inputs map[ConversationContext]*InteractiveStdin
}

func (r *testThreadRegistry) register(context ConversationContext, input *InteractiveStdin) {
	input.mu.Lock()
	defer input.mu.Unlock()
	if input.closed {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.inputs == nil {
		r.inputs = make(map[ConversationContext]*InteractiveStdin)
	}
	r.inputs[context] = input
}

func (r *testThreadRegistry) lookup(context ConversationContext) *InteractiveStdin {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.inputs[context]
}

func (r *testThreadRegistry) unregister(context ConversationContext, input *InteractiveStdin) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.inputs[context] == input {
		delete(r.inputs, context)
	}
}

type testLifecycle struct {
	registry *testThreadRegistry
	context  ConversationContext
}

func (l testLifecycle) StdinReady(input *InteractiveStdin)  { l.registry.register(l.context, input) }
func (l testLifecycle) StdinClosed(input *InteractiveStdin) { l.registry.unregister(l.context, input) }

func testRunWithInput(command Cmd, timeout int, idle time.Duration, initial string, context ConversationContext, registry *testThreadRegistry) int {
	if registry == nil || context.ChannelID == "" || context.RootThreadTimestamp == "" {
		return runWithLifecycleInput(command, timeout, idle, initial, context, nil)
	}
	return runWithLifecycleInput(command, timeout, idle, initial, context, testLifecycle{registry: registry, context: context})
}

func testExecutorWithLifecycle(ctx context.Context, rq chan *CommandInput, wq chan *CommandOutput, cfgs []*CommandConfig, runnerFactory RunnerFactory, registry *testThreadRegistry) {
	matchers := buildMatchers(ExecutionConfigs(cfgs), normalizeRunnerFactory(runnerFactory))
	for input := range rq {
		var lifecycle StdinLifecycle
		if registry != nil {
			lifecycle = testLifecycle{
				registry: registry,
				context:  input.ConversationContext,
			}
		}
		executeCommandInput(ctx, input, matchers, runnerFactory, wq, lifecycle)
	}
}

package cmd

import (
	"context"
	"time"
)

type testExecutionConfig struct {
	Index            int
	StdinIdleTimeout time.Duration
	TTY              bool
	Timeout          time.Duration
	AllowInChain     bool
	InteractiveStdin bool
	InputBodyMode    InputBodyMode
	Keyword          string
	Command          string
	Runner           string
	Method           string
	URL              string
	Headers          map[string]string
	Body             string
	Dispatch         DispatchMode
}

type testCommandConfig struct {
	*testExecutionConfig
	Replies     []*testCommandConfig
	ThreadStdin bool
}

func newTestCommandConfig(config *testExecutionConfig) *testCommandConfig {
	return &testCommandConfig{testExecutionConfig: config}
}

func testRuntimeCommand(config *testExecutionConfig, runner CommandRunner) *Command {
	definition := *config
	if definition.Runner == RunnerHTTP {
		definition.Dispatch = DispatchExecutor
	}
	return newTestCommand(definition, runner, nil)
}

func testCommandSet(configs []*testCommandConfig, factory func(*testExecutionConfig) CommandRunner) *CommandSet {
	commands := make([]*Command, 0, len(configs))
	for _, config := range configs {
		if config == nil || config.testExecutionConfig == nil {
			continue
		}
		definition := config.testExecutionConfig
		var runner CommandRunner
		if factory != nil {
			runner = factory(definition)
		}
		replies := testCommandSet(config.Replies, factory)
		if config.ThreadStdin {
			if replies == nil {
				replies = NewCommandSet(nil)
			}
			replies.commands = append(replies.commands, newTestCommand(testExecutionConfig{
				Index: 1000, Keyword: "*", InputBodyMode: InputBodyRawStdin, Runner: RunnerStdinReply, Command: "stdin-reply",
				Dispatch: DispatchRunner,
			}, NewStdinReplyRunner(), nil))
		}
		runtimeConfig := *definition
		if runtimeConfig.Runner == RunnerHTTP {
			runtimeConfig.Dispatch = DispatchExecutor
		}
		command := newTestCommand(runtimeConfig, runner, replies)
		commands = append(commands, command)
	}
	return NewCommandSet(commands)
}

func newTestConversationRouter(configs []*testCommandConfig, resolve RootInputResolver, enqueue func(*CommandInput) bool, routeCapacity int) *ConversationRouter {
	commands := testCommandSet(configs, nil)
	observeCommandSet(commands, make(chan *observedOutput, 100))
	return NewConversationRouter(&StdinStore{}, commands, resolve, newTestDispatcher(enqueue), routeCapacity)
}

func newTestDispatcher(enqueue func(*CommandInput) bool) *CommandDispatcher {
	return newTestCommandDispatcher(context.Background(), enqueue)
}

func newTestCommandDispatcher(ctx context.Context, enqueue func(*CommandInput) bool) *CommandDispatcher {
	return NewCommandDispatcher(ctx, NewExecutor(), &StdinStore{}, &ConversationLocks{}, enqueue)
}

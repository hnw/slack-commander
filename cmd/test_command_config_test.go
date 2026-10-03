package cmd

import (
	"context"
	"time"
)

// testExecutionConfig is a test fixture for constructing runtime Commands.
// Production configuration is intentionally split across the runtime types.
type testExecutionConfig struct {
	Index               int
	StdinIdleTimeout    int
	TTY                 bool
	Timeout             int
	OutputFlushInterval time.Duration
	AllowInChain        bool
	InteractiveStdin    bool
	InputBodyMode       InputBodyMode
	Keyword             string
	Command             string
	Runner              string
	Method              string
	URL                 string
	Headers             map[string]string
	Body                string
	ReplyConfig         interface{}
	SystemReplyConfig   interface{}
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
	dispatch := DispatchQueue
	if config.Runner == RunnerHTTP {
		dispatch = DispatchExecutor
	}
	return NewCommand(
		CommandConfig{
			Index:               config.Index,
			MatcherConfig:       MatcherConfig{RawMatcherConfig: RawMatcherConfig{Keyword: config.Keyword}},
			RunnerConfig:        RunnerConfig{RawRunnerConfig: RawRunnerConfig{Runner: config.Runner, Command: config.Command, Method: config.Method, URL: config.URL, Headers: config.Headers, Body: config.Body}},
			ParserConfig:        ParserConfig{InputBodyMode: config.InputBodyMode, AllowInChain: config.AllowInChain},
			ExecutorConfig:      ExecutorConfig{Timeout: config.Timeout, StdinIdleTimeout: config.StdinIdleTimeout, TTY: config.TTY, InteractiveStdin: config.InteractiveStdin},
			OutputFlushInterval: config.OutputFlushInterval,
			ReplyConfig:         config.ReplyConfig,
			SystemReplyConfig:   config.SystemReplyConfig,
			Dispatch:            dispatch,
		},
		runner,
		nil,
	)
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
			replies.commands = append(replies.commands, NewCommand(CommandConfig{
				Index:         1000,
				MatcherConfig: MatcherConfig{RawMatcherConfig: RawMatcherConfig{Keyword: "*"}},
				ParserConfig:  ParserConfig{InputBodyMode: InputBodyRawStdin},
				RunnerConfig:  RunnerConfig{RawRunnerConfig: RawRunnerConfig{Runner: RunnerStdinReply, Command: "stdin-reply"}},
				Dispatch:      DispatchRunner,
			}, NewStdinReplyRunner(&StdinStore{}), nil))
		}
		command := testRuntimeCommand(definition, runner)
		command.replies = replies
		commands = append(commands, command)
	}
	return NewCommandSet(commands)
}

func newTestConversationRouter(configs []*testCommandConfig, resolve RootInputResolver, enqueue func(*CommandInput) bool, routeCapacity int) *ConversationRouter {
	commands := testCommandSet(configs, nil)
	return NewConversationRouterWithRootInputResolver(commands, resolve, newTestDispatcher(enqueue), routeCapacity)
}

func newTestDispatcher(enqueue func(*CommandInput) bool) *CommandDispatcher {
	return newTestCommandDispatcher(context.Background(), 100, enqueue)
}

func newTestCommandDispatcher(ctx context.Context, capacity int, enqueue func(*CommandInput) bool) *CommandDispatcher {
	outputs := make(chan *CommandOutput, capacity)
	return NewCommandDispatcher(ctx, NewExecutor(outputs), outputs, &StdinStore{}, &ConversationLocks{}, enqueue)
}

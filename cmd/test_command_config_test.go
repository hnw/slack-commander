package cmd

import "time"

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
	return NewCommand(
		CommandConfig{
			Index:               config.Index,
			MatcherConfig:       MatcherConfig{Keyword: config.Keyword},
			RunnerConfig:        RunnerConfig{Runner: config.Runner, Command: config.Command, Method: config.Method, URL: config.URL, Headers: config.Headers, Body: config.Body},
			ExecutorConfig:      ExecutorConfig{Timeout: config.Timeout, StdinIdleTimeout: config.StdinIdleTimeout, TTY: config.TTY, InteractiveStdin: config.InteractiveStdin, InputBodyMode: config.InputBodyMode, AllowInChain: config.AllowInChain},
			OutputFlushInterval: config.OutputFlushInterval,
			ReplyConfig:         config.ReplyConfig,
			SystemReplyConfig:   config.SystemReplyConfig,
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
				Index:               1000,
				MatcherConfig:       MatcherConfig{Keyword: "*"},
				SyntheticStdinReply: true,
			}, nil, nil))
		}
		command := testRuntimeCommand(definition, runner)
		command.replies = replies
		commands = append(commands, command)
	}
	return NewCommandSet(commands)
}

func newTestConversationRouter(configs []*testCommandConfig, resolve RootInputResolver, enqueue func(*CommandInput) bool, routeCapacity int) *ConversationRouter {
	return NewConversationRouterWithRootInputResolver(testCommandSet(configs, nil), resolve, enqueue, routeCapacity, &StdinStore{})
}

package cmd

import "time"

// MatcherConfig contains only the data needed to recognize a command.
type MatcherConfig struct {
	Keyword string `toml:"keyword"`
}

// RunnerConfig contains the runner-specific command definition.
type RunnerConfig struct {
	Runner  string `toml:"runner"`
	Command string `toml:"command"`

	Method  string            `toml:"method"`
	URL     string            `toml:"url"`
	Headers map[string]string `toml:"headers"`
	Body    string            `toml:"body"`
}

// ExecutorConfig contains execution policy after a command has matched.
type ExecutorConfig struct {
	Timeout          int           `toml:"timeout"`
	StdinIdleTimeout int           `toml:"stdin_idle_timeout"`
	TTY              bool          `toml:"tty"`
	InteractiveStdin bool          `toml:"-"`
	InputBodyMode    InputBodyMode `toml:"-"`
	AllowInChain     bool          `toml:"-"`
}

// CommandConfig is one fully resolved, validated command definition.
// Replies forms the resolved configuration tree.
type CommandConfig struct {
	MatcherConfig
	RunnerConfig
	ExecutorConfig
	OutputFlushInterval time.Duration
	ReplyConfig         interface{}
	SystemReplyConfig   interface{}
	ThreadReplyMode     ThreadReplyMode
	Replies             []*CommandConfig
}

// Command is an instantiated runtime command.
// Config replies are converted to the runtime CommandSet and are not retained.
type Command struct {
	config  CommandConfig
	matcher Matcher
	runner  CommandRunner
	replies *CommandSet
}

func newCommand(config CommandConfig, runner CommandRunner, replies *CommandSet) *Command {
	if runner == nil {
		runner = NewExecRunner()
	}
	config.Replies = nil
	return &Command{
		config:  config,
		matcher: *newMatcher(config.MatcherConfig),
		runner:  runner,
		replies: replies,
	}
}

// NewCommand instantiates one runtime command from resolved configuration.
func NewCommand(config CommandConfig, runner CommandRunner, replies *CommandSet) *Command {
	return newCommand(config, runner, replies)
}

func (c *Command) match(args []string) []string {
	wildcard, ok := c.matcher.match(args)
	if !ok {
		return nil
	}
	if c.config.Runner == RunnerHTTP {
		return buildHTTPArgs(containsWildcard(c.matcher.keywords), wildcard)
	}
	return buildCommandArgs(c.config.Command, containsWildcard(c.matcher.keywords), wildcard)
}

func (c *Command) hasTrailingWildcard() bool {
	return c.matcher.hasTrailingWildcard()
}

// CommandSet is the runtime matching unit shared by execution and routing.
type CommandSet struct {
	commands []*Command
}

// NewCommandSet groups commands that may be matched against the same input.
func NewCommandSet(commands []*Command) *CommandSet {
	return &CommandSet{commands: commands}
}

// Match returns the first matching command and its runner arguments.
func (s *CommandSet) Match(input *parsedCommand) (*Command, []string) {
	if s == nil || input == nil {
		return nil, nil
	}
	for _, command := range s.commands {
		if command == nil {
			continue
		}
		if args := command.match(input.args); len(args) > 0 {
			return command, args
		}
	}
	return nil, nil
}

// MatchSingle matches exactly one complete command line for route ownership.
func (s *CommandSet) MatchSingle(text string) *Command {
	cmdMsg, _ := splitCommandInput(text)
	commands, err := parseCommands(cmdMsg)
	if err != nil || len(commands) != 1 {
		return nil
	}
	command, _ := s.Match(commands[0])
	return command
}

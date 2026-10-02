package cmd

import (
	"slices"
	"time"
)

// RawMatcherConfig decodes matcher settings from TOML.
type RawMatcherConfig struct {
	Keyword string `toml:"keyword"`
}

// MatcherConfig contains the settings needed to match a command.
type MatcherConfig struct {
	RawMatcherConfig
}

// ParserConfig contains input parsing policy.
type ParserConfig struct {
	AllowInChain  bool
	InputBodyMode InputBodyMode
}

// RawRunnerConfig decodes runner settings from TOML.
type RawRunnerConfig struct {
	Runner  string            `toml:"runner"`
	Command string            `toml:"command"`
	Method  string            `toml:"method"`
	URL     string            `toml:"url"`
	Headers map[string]string `toml:"headers"`
	Body    string            `toml:"body"`
}

// RunnerConfig contains the runner-specific command definition.
type RunnerConfig struct {
	RawRunnerConfig
}

// ExecutorConfig contains execution policy after a command has matched.
type ExecutorConfig struct {
	Timeout          int
	StdinIdleTimeout int
	TTY              bool
	InteractiveStdin bool
}

// CommandConfig is one fully resolved, validated command definition.
// Replies forms the resolved configuration tree.
type CommandConfig struct {
	Index int
	MatcherConfig
	ParserConfig
	RunnerConfig
	ExecutorConfig
	OutputFlushInterval time.Duration
	ReplyConfig         interface{}
	SystemReplyConfig   interface{}
	DispatchPolicy      DispatchPolicy
	Replies             []*CommandConfig
}

// DispatchPolicy controls how a matched reply command is dispatched.
type DispatchPolicy int

const (
	// DispatchQueued sends the command through the worker queue.
	DispatchQueued DispatchPolicy = iota
	// DispatchDirect invokes the command runner directly.
	DispatchDirect
)

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
func (s *CommandSet) Match(input *parsedCommand, allowedIndexes []int) (*Command, []string) {
	if s == nil || input == nil {
		return nil, nil
	}
	return s.match(allowedIndexes, func(*Command) *parsedCommand { return input })
}

func (s *CommandSet) match(allowedIndexes []int, candidate func(*Command) *parsedCommand) (*Command, []string) {
	if s == nil || candidate == nil {
		return nil, nil
	}
	for _, command := range s.commands {
		if command == nil {
			continue
		}
		if !slices.Contains(allowedIndexes, command.config.Index) {
			continue
		}
		input := candidate(command)
		if input == nil {
			continue
		}
		args := command.match(input.args)
		if len(args) == 0 {
			continue
		}
		return command, args
	}
	return nil, nil
}

// MatchSingle matches exactly one complete command line for route ownership.
func (s *CommandSet) MatchSingle(text string, allowedIndexes []int) *Command {
	cmdMsg, _ := splitCommandInput(text)
	commands, err := parseCommands(cmdMsg)
	if err != nil || len(commands) != 1 {
		return nil
	}
	command, _ := s.Match(commands[0], allowedIndexes)
	return command
}

// MatchReply matches a reply according to each command's InputBodyMode.
func (s *CommandSet) MatchReply(text string, allowedIndexes []int) (*Command, []string) {
	if s == nil {
		return nil, nil
	}
	rawInput := &parsedCommand{args: []string{text}}
	var commandInput *parsedCommand
	commandInputResolved := false
	return s.match(allowedIndexes, func(command *Command) *parsedCommand {
		if command.config.InputBodyMode == InputBodyRawStdin {
			return rawInput
		}
		if !commandInputResolved {
			commandInputResolved = true
			cmdMsg, _ := splitCommandInput(text)
			parsed, _ := parseCommands(cmdMsg)
			if len(parsed) > 0 {
				commandInput = parsed[0]
			}
		}
		return commandInput
	})
}

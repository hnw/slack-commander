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
	Dispatch            CommandDispatch
	Replies             []*CommandConfig
}

// CommandDispatch defines how a matched command is dispatched.
type CommandDispatch int

const (
	// CommandDispatchQueue routes the input through the worker queue to the Executor.
	CommandDispatchQueue CommandDispatch = iota
	// CommandDispatchExecutor bypasses the worker queue and routes the input to the Executor.
	CommandDispatchExecutor
	// CommandDispatchRunner bypasses the Executor and invokes the runner directly.
	CommandDispatchRunner
)

// DispatchTarget identifies the dispatch destination for a resolved input.
type DispatchTarget int

const (
	// DispatchNone indicates that the resolved input has no matched commands to dispatch.
	DispatchNone DispatchTarget = iota
	// DispatchQueue routes the input through the worker queue to the Executor.
	DispatchQueue
	// DispatchExecutor bypasses the worker queue and routes the input to the Executor.
	DispatchExecutor
	// DispatchRunner bypasses the Executor and invokes the runner directly.
	DispatchRunner
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

// CommandSet groups runtime commands that may match the same input.
type CommandSet struct {
	commands []*Command
}

// ResolvedCommand contains a command-line part and its resolved runtime command.
type ResolvedCommand struct {
	Part    *commandPart
	Command *Command
	Args    []string
}

// ResolvedInput contains the parsed input and resolved commands.
type ResolvedInput struct {
	Commands  []ResolvedCommand
	ParseErr  error
	StdinText string
}

// SingleCommand returns the command when the input is a valid single command.
func (p *ResolvedInput) SingleCommand() *Command {
	if p == nil || p.ParseErr != nil || len(p.Commands) != 1 {
		return nil
	}
	return p.Commands[0].Command
}

// DispatchTarget returns the aggregate dispatch destination of the matched commands.
func (p *ResolvedInput) DispatchTarget() (DispatchTarget, bool) {
	if p == nil {
		return DispatchNone, false
	}
	target := DispatchNone
	for _, resolved := range p.Commands {
		if resolved.Command == nil {
			continue
		}
		current := resolved.Command.config.Dispatch
		switch current {
		case CommandDispatchQueue:
			target = DispatchQueue
		case CommandDispatchExecutor:
			if target != DispatchQueue {
				target = DispatchExecutor
			}
		case CommandDispatchRunner:
			if len(p.Commands) != 1 {
				return DispatchNone, false
			}
			return DispatchRunner, true
		default:
			return DispatchNone, false
		}
	}
	return target, true
}

// NewCommandSet groups commands that may be matched against the same input.
func NewCommandSet(commands []*Command) *CommandSet {
	return &CommandSet{commands: commands}
}

// Match returns the first matching command and its runner arguments.
func (s *CommandSet) Match(input *commandPart, allowedIndexes []int) (*Command, []string) {
	if s == nil || input == nil {
		return nil, nil
	}
	return s.match(allowedIndexes, func(*Command) *commandPart { return input })
}

// ResolveInput parses the input and resolves each command before dispatch.
func (s *CommandSet) ResolveInput(text string, allowedIndexes []int) *ResolvedInput {
	cmdMsg, stdinText := splitCommandInput(text)
	commands, parseErr := parseCommands(cmdMsg)
	input := &ResolvedInput{ParseErr: parseErr, StdinText: stdinText}
	rawInput := &commandPart{args: []string{text}}
	firstCommand, firstArgs := s.match(allowedIndexes, func(command *Command) *commandPart {
		if command.config.InputBodyMode == InputBodyRawStdin {
			return rawInput
		}
		if len(commands) == 0 {
			return nil
		}
		return commands[0]
	})
	if firstCommand != nil && firstCommand.config.InputBodyMode == InputBodyRawStdin {
		input.Commands = []ResolvedCommand{{Part: rawInput, Command: firstCommand, Args: firstArgs}}
		input.ParseErr = nil
		input.StdinText = text
		return input
	}
	input.Commands = make([]ResolvedCommand, len(commands))
	for i, part := range commands {
		resolved := ResolvedCommand{Part: part}
		if i == 0 {
			resolved.Command, resolved.Args = firstCommand, firstArgs
		} else {
			resolved.Command, resolved.Args = s.Match(part, allowedIndexes)
		}
		input.Commands[i] = resolved
	}
	return input
}

func (s *CommandSet) match(allowedIndexes []int, candidate func(*Command) *commandPart) (*Command, []string) {
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

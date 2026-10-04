package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/pelletier/go-toml/v2"

	"github.com/hnw/slack-commander/cmd"
	"github.com/hnw/slack-commander/pubsub"
)

type PubSubConfig = pubsub.Config // TOMLデコード対象のためexportedにする

// Duration decodes Go duration syntax from TOML strings.
type Duration time.Duration

func (d *Duration) UnmarshalText(text []byte) error {
	value, err := time.ParseDuration(string(text))
	if err != nil {
		return err
	}
	*d = Duration(value)
	return nil
}

type Config struct {
	PubSubConfig
	NumWorkers          int       `toml:"num_workers"`
	OutputFlushInterval *Duration `toml:"output_flush_interval"`
	Commands            []*RawCommandConfig
	commandConfigs      []*resolvedCommandConfig
}

type resolvedCommandConfig struct {
	cmd.CommandConfig
	ReplyConfig         pubsub.ReplyConfig
	OutputFlushInterval time.Duration
	Replies             []*resolvedCommandConfig
}

type RawCommandConfig struct {
	cmd.RawMatcherConfig
	cmd.RawRunnerConfig
	RawExecutorConfig
	pubsub.ReplyConfig
	pubsub.RawListenerConfig
	Interaction         string    `toml:"interaction"`
	OutputFlushInterval *Duration `toml:"output_flush_interval"`
	Replies             []*RawCommandConfig
}

// RawExecutorConfig preserves whether TOML execution settings were omitted.
type RawExecutorConfig struct {
	Timeout          *Duration `toml:"timeout"`
	StdinIdleTimeout *Duration `toml:"stdin_idle_timeout"`
	TTY              *bool     `toml:"tty"`
}

func loadConfig(path string) (*Config, error) {
	// #nosec G304 -- The local CLI caller explicitly selects the configuration file.
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = file.Close()
	}()

	cfg := &Config{NumWorkers: 1}
	if err := decodeConfig(file, cfg); err != nil {
		return nil, err
	}
	if err := resolveConfig(cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}

func decodeConfig(r io.Reader, cfg *Config) error {
	decoder := toml.NewDecoder(r)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(cfg); err != nil {
		return formatTOMLError(err)
	}
	return nil
}

func formatTOMLError(err error) error {
	var strictErr *toml.StrictMissingError
	if errors.As(err, &strictErr) {
		return errors.New(strictErr.String())
	}

	var decodeErr *toml.DecodeError
	if errors.As(err, &decodeErr) {
		return errors.New(decodeErr.String())
	}

	return err
}

func resolveReplyConfig(parent pubsub.ReplyConfig, reply *RawCommandConfig) *pubsub.ReplyConfig {
	replyConfig := &pubsub.ReplyConfig{
		Username:       parent.Username,
		IconEmoji:      parent.IconEmoji,
		IconURL:        parent.IconURL,
		ReplyBroadcast: parent.ReplyBroadcast,
		OutputFormat:   parent.OutputFormat,
	}
	if reply.Username != "" {
		replyConfig.Username = reply.Username
	}
	if reply.IconEmoji != "" {
		replyConfig.IconEmoji = reply.IconEmoji
	}
	if reply.IconURL != "" {
		replyConfig.IconURL = reply.IconURL
	}
	if reply.ReplyBroadcast != nil {
		replyConfig.ReplyBroadcast = reply.ReplyBroadcast
	}
	if reply.OutputFormat != "" {
		replyConfig.OutputFormat = reply.OutputFormat
	}
	return replyConfig
}

func buildCommandSet(configs []*resolvedCommandConfig, factory cmd.RunnerFactory, queue chan *pubsub.CommandOutput) *cmd.CommandSet {
	commands := make([]*cmd.Command, 0, len(configs))
	for _, config := range configs {
		commands = append(commands, buildCommand(config, factory, queue))
	}
	return cmd.NewCommandSet(commands)
}

func buildCommand(config *resolvedCommandConfig, factory cmd.RunnerFactory, queue chan *pubsub.CommandOutput) *cmd.Command {
	replies := buildCommandSet(config.Replies, factory, queue)
	return cmd.NewCommand(config.CommandConfig, factory(config.RunnerConfig), replies, pubsub.NewSlackOutputHandler(queue, config.ReplyConfig, config.OutputFlushInterval))
}

func resolveOutputFlushInterval(value *Duration, inherited time.Duration) time.Duration {
	if value != nil {
		return time.Duration(*value)
	}
	return inherited
}

func resolveCommandConfig(raw *RawCommandConfig, inheritedOutputFlushInterval time.Duration, commandNumber int) (*resolvedCommandConfig, error) {
	target := fmt.Sprintf("command keyword '%s'", raw.Keyword)
	if strings.TrimSpace(raw.Keyword) == "" {
		target = fmt.Sprintf("command #%d", commandNumber)
	}
	if validationErr := validateReplyConfig(&raw.ReplyConfig); validationErr != nil {
		return nil, fmt.Errorf("%s: %w", target, validationErr)
	}
	interaction, err := normalizeInteraction(raw.Interaction)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", target, err)
	}
	matcherConfig := cmd.MatcherConfig{RawMatcherConfig: raw.RawMatcherConfig}
	runnerConfig := cmd.RunnerConfig{RawRunnerConfig: raw.RawRunnerConfig}
	if _, err := normalizeRunner(&runnerConfig); err != nil {
		return nil, fmt.Errorf("%s: %w", target, err)
	}
	parserConfig, executorConfig := resolveInteraction(interaction, resolveExecutorConfig(cmd.ExecutorConfig{}, raw.RawExecutorConfig))
	outputFlushInterval := resolveOutputFlushInterval(raw.OutputFlushInterval, inheritedOutputFlushInterval)
	if err := validateCommandDefinition(matcherConfig, runnerConfig, executorConfig, outputFlushInterval); err != nil {
		return nil, fmt.Errorf("%s: %w", target, err)
	}
	if runnerConfig.Runner == cmd.RunnerHTTP && interaction == cmd.InteractionStdin {
		return nil, fmt.Errorf("%s: http runner does not support interaction %q; use %q or %q", target, cmd.InteractionStdin, cmd.InteractionOneshot, cmd.InteractionCommand)
	}
	config := &resolvedCommandConfig{CommandConfig: cmd.CommandConfig{MatcherConfig: matcherConfig, ParserConfig: parserConfig, RunnerConfig: runnerConfig, ExecutorConfig: executorConfig, Dispatch: commandDispatch(runnerConfig.Runner)}, OutputFlushInterval: outputFlushInterval, ReplyConfig: raw.ReplyConfig}
	configuredReplies := make([]*resolvedCommandConfig, 0, len(raw.Replies))
	for i, reply := range raw.Replies {
		resolved, err := resolveReplyCommandConfig(config, reply, i+1)
		if err != nil {
			return nil, err
		}
		configuredReplies = append(configuredReplies, resolved)
	}
	switch interaction {
	case cmd.InteractionCommand:
		config.Replies = configuredReplies
	case cmd.InteractionStdin:
		replyParserConfig := parserConfig
		replyParserConfig.InputBodyMode = cmd.InputBodyRawStdin
		config.Replies = []*resolvedCommandConfig{{CommandConfig: cmd.CommandConfig{
			MatcherConfig:  cmd.MatcherConfig{RawMatcherConfig: cmd.RawMatcherConfig{Keyword: "*"}},
			ParserConfig:   replyParserConfig,
			RunnerConfig:   cmd.RunnerConfig{RawRunnerConfig: cmd.RawRunnerConfig{Runner: cmd.RunnerStdinReply, Command: "stdin-reply"}},
			ExecutorConfig: executorConfig,
			Dispatch:       cmd.DispatchRunner,
		}, ReplyConfig: config.ReplyConfig, OutputFlushInterval: config.OutputFlushInterval}}
	}
	return config, nil
}

func resolveReplyCommandConfig(parent *resolvedCommandConfig, raw *RawCommandConfig, replyNumber int) (*resolvedCommandConfig, error) {
	target := fmt.Sprintf("reply keyword '%s'", raw.Keyword)
	if strings.TrimSpace(raw.Keyword) == "" {
		target = fmt.Sprintf("reply #%d of command keyword '%s'", replyNumber, parent.Keyword)
	}
	if raw.Interaction != "" {
		return nil, fmt.Errorf("%s: interaction is not allowed on reply commands", target)
	}
	if len(raw.Replies) > 0 {
		return nil, fmt.Errorf("%s: nested replies are not supported", target)
	}
	matcherConfig := cmd.MatcherConfig{RawMatcherConfig: raw.RawMatcherConfig}
	runnerConfig := cmd.RunnerConfig{RawRunnerConfig: raw.RawRunnerConfig}
	if runnerConfig.Runner == "" {
		runnerConfig.Runner = parent.Runner
	}
	if _, err := normalizeRunner(&runnerConfig); err != nil {
		return nil, fmt.Errorf("%s: %w", target, err)
	}
	executorConfig := resolveExecutorConfig(parent.ExecutorConfig, raw.RawExecutorConfig)
	outputFlushInterval := resolveOutputFlushInterval(raw.OutputFlushInterval, parent.OutputFlushInterval)
	resolvedReplyConfig := resolveReplyConfig(parent.ReplyConfig, raw)
	if err := validateReplyConfig(resolvedReplyConfig); err != nil {
		return nil, fmt.Errorf("%s: %w", target, err)
	}
	if err := validateCommandDefinition(matcherConfig, runnerConfig, executorConfig, outputFlushInterval); err != nil {
		return nil, fmt.Errorf("%s: %w", target, err)
	}
	return &resolvedCommandConfig{CommandConfig: cmd.CommandConfig{MatcherConfig: matcherConfig, ParserConfig: parent.ParserConfig, RunnerConfig: runnerConfig, ExecutorConfig: executorConfig, Dispatch: commandDispatch(runnerConfig.Runner)}, OutputFlushInterval: outputFlushInterval, ReplyConfig: *resolvedReplyConfig}, nil
}

func commandDispatch(runner string) cmd.DispatchMode {
	if runner == cmd.RunnerHTTP {
		return cmd.DispatchExecutor
	}
	return cmd.DispatchQueue
}

func resolveExecutorConfig(base cmd.ExecutorConfig, raw RawExecutorConfig) cmd.ExecutorConfig {
	if raw.Timeout != nil {
		base.Timeout = time.Duration(*raw.Timeout)
	}
	if raw.StdinIdleTimeout != nil {
		base.StdinIdleTimeout = time.Duration(*raw.StdinIdleTimeout)
	}
	if raw.TTY != nil {
		base.TTY = *raw.TTY
	}
	return base
}

func resolveInteraction(interaction string, executorConfig cmd.ExecutorConfig) (cmd.ParserConfig, cmd.ExecutorConfig) {
	parserConfig := cmd.ParserConfig{}
	switch interaction {
	case cmd.InteractionStdin:
		parserConfig.AllowInChain = true
		executorConfig.InteractiveStdin = true
		parserConfig.InputBodyMode = cmd.InputBodyStdin
		return parserConfig, executorConfig
	case cmd.InteractionCommand:
		parserConfig.AllowInChain = false
		executorConfig.InteractiveStdin = false
		parserConfig.InputBodyMode = cmd.InputBodyArgument
		return parserConfig, executorConfig
	default:
		parserConfig.AllowInChain = true
		executorConfig.InteractiveStdin = false
		parserConfig.InputBodyMode = cmd.InputBodyStdin
		return parserConfig, executorConfig
	}
}

func resolveConfig(cfg *Config) error {
	var validationErrors []error
	if strings.TrimSpace(cfg.SlackBotToken) == "" {
		validationErrors = append(validationErrors, errors.New("slack_bot_token is required"))
	}
	if strings.TrimSpace(cfg.SlackAppToken) == "" {
		validationErrors = append(validationErrors, errors.New("slack_app_token is required"))
	}
	if cfg.NumWorkers < 1 {
		validationErrors = append(validationErrors, fmt.Errorf("num_workers must be >= 1 (got %d)", cfg.NumWorkers))
	}
	if err := validateReplyConfig(&cfg.ReplyConfig); err != nil {
		validationErrors = append(validationErrors, err)
	}
	outputFlushInterval := resolveOutputFlushInterval(
		cfg.OutputFlushInterval,
		pubsub.DefaultOutputFlushInterval,
	)
	if outputFlushInterval < 0 {
		validationErrors = append(validationErrors, errors.New("output_flush_interval must be >= 0"))
	}
	if err := validateOpenAccess(cfg); err != nil {
		validationErrors = append(validationErrors, err)
	}
	if containsString(cfg.AllowedUserIDs, "USLACKBOT") {
		validationErrors = append(validationErrors, errors.New("USLACKBOT cannot be used in allowed_user_ids; use accept_reminder for Slack Reminder messages"))
	}
	if len(validationErrors) > 0 {
		return formatValidationErrors(validationErrors)
	}

	cfg.commandConfigs = make([]*resolvedCommandConfig, 0, len(cfg.Commands))
	cfg.ListenerConfigs = make([]pubsub.ListenerConfig, 0)
	listenerDefaults := pubsub.RawListenerConfig{AllowedUserIDs: cfg.AllowedUserIDs, AllowedChannelIDs: cfg.AllowedChannelIDs}
	nextCommandIndex := 0
	for i, c := range cfg.Commands {
		resolved, err := resolveCommandConfig(c, outputFlushInterval, i+1)
		if err != nil {
			validationErrors = append(validationErrors, err)
			continue
		}
		var listeners []pubsub.ListenerConfig
		next, err := assignCommandIndexes(c, resolved, listenerDefaults, nextCommandIndex, &listeners)
		if err != nil {
			validationErrors = append(validationErrors, err)
			continue
		}
		nextCommandIndex = next
		cfg.ListenerConfigs = append(cfg.ListenerConfigs, listeners...)
		cfg.commandConfigs = append(cfg.commandConfigs, resolved)
	}
	return formatValidationErrors(validationErrors)
}

func formatValidationErrors(validationErrors []error) error {
	if len(validationErrors) == 0 {
		return nil
	}
	for i, err := range validationErrors {
		validationErrors[i] = fmt.Errorf("  - %w", err)
	}
	return fmt.Errorf("invalid configuration:\n%w", errors.Join(validationErrors...))
}

func assignCommandIndexes(raw *RawCommandConfig, resolved *resolvedCommandConfig, inherited pubsub.RawListenerConfig, next int, flat *[]pubsub.ListenerConfig) (int, error) {
	resolved.Index = next
	listenerConfig, err := resolveListenerConfig(raw.RawListenerConfig, inherited)
	if err != nil {
		return next, fmt.Errorf("command keyword '%s': %w", raw.Keyword, err)
	}
	*flat = append(*flat, pubsub.ListenerConfig{CommandIndex: next, RawListenerConfig: listenerConfig})
	next++

	resolvedReplyACLs := make([]pubsub.RawListenerConfig, len(raw.Replies))
	for i, rawReply := range raw.Replies {
		resolvedReplyACLs[i], err = resolveListenerConfig(rawReply.RawListenerConfig, listenerConfig)
		if err != nil {
			return next, fmt.Errorf("reply keyword '%s': %w", rawReply.Keyword, err)
		}
	}
	for i, reply := range resolved.Replies {
		replyACL := listenerConfig
		if resolved.InteractiveStdin {
			replyACL.AllowedUserIDs = append([]string(nil), listenerConfig.AllowedUserIDs...)
			replyACL.AllowedChannelIDs = append([]string(nil), listenerConfig.AllowedChannelIDs...)
		} else {
			replyACL = resolvedReplyACLs[i]
		}
		reply.Index = next
		*flat = append(*flat, pubsub.ListenerConfig{CommandIndex: next, IsReply: true, RawListenerConfig: replyACL})
		next++
	}
	return next, nil
}

func resolveListenerConfig(raw, inherited pubsub.RawListenerConfig) (pubsub.RawListenerConfig, error) {
	resolved := pubsub.RawListenerConfig{AcceptReminder: raw.AcceptReminder}
	var err error
	resolved.AllowedUserIDs, err = resolveAllowedIDs(raw.AllowedUserIDs, inherited.AllowedUserIDs, "allowed_user_ids")
	if err != nil {
		return pubsub.RawListenerConfig{}, err
	}
	resolved.AllowedChannelIDs, err = resolveAllowedIDs(raw.AllowedChannelIDs, inherited.AllowedChannelIDs, "allowed_channel_ids")
	if err != nil {
		return pubsub.RawListenerConfig{}, err
	}
	return resolved, nil
}

func resolveAllowedIDs(value []string, inherited []string, name string) ([]string, error) {
	if len(value) == 0 {
		return inherited, nil
	}
	resolved := append([]string(nil), value...)
	if len(inherited) == 0 {
		return resolved, nil
	}
	for _, id := range resolved {
		if !containsString(inherited, id) {
			return nil, fmt.Errorf("%s value %q is not allowed by its parent", name, id)
		}
	}
	return resolved, nil
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func validateOpenAccess(cfg *Config) error {
	if len(cfg.AllowedUserIDs) == 0 && len(cfg.AllowedChannelIDs) == 0 && !cfg.AllowUnsafeOpenAccess {
		return openAccessError()
	}
	return nil
}

func openAccessError() error {
	return errors.New(
		"open access is disabled by default: set allowed_user_ids and/or allowed_channel_ids, " +
			"or set allow_unsafe_open_access=true to keep old behavior",
	)
}

func validateReplyConfig(cfg *pubsub.ReplyConfig) error {
	switch cfg.OutputFormat {
	case "", pubsub.OutputFormatPlain, pubsub.OutputFormatMonospaced, pubsub.OutputFormatMarkdown:
		return nil
	default:
		return fmt.Errorf("unknown output_format %q; valid values are %q, %q, and %q", cfg.OutputFormat, pubsub.OutputFormatPlain, pubsub.OutputFormatMonospaced, pubsub.OutputFormatMarkdown)
	}
}

func validateCommandDefinition(matcher cmd.MatcherConfig, runnerConfig cmd.RunnerConfig, executorConfig cmd.ExecutorConfig, outputFlushInterval time.Duration) error {
	if strings.TrimSpace(matcher.Keyword) == "" {
		return errors.New("keyword is required")
	}
	if err := validateKeywordWildcards(matcher.Keyword); err != nil {
		return err
	}
	if err := validateExecutorDurations(executorConfig); err != nil {
		return err
	}
	if outputFlushInterval < 0 {
		return errors.New("output_flush_interval must be >= 0")
	}
	runner := runnerConfig.Runner
	if err := validateTTY(executorConfig); err != nil {
		return err
	}
	if executorConfig.TTY && runner == cmd.RunnerHTTP {
		return errors.New("tty is not supported for http runner")
	}
	if runner != cmd.RunnerHTTP {
		if strings.TrimSpace(runnerConfig.Command) == "" {
			return errors.New("command is required")
		}
		if strings.HasPrefix(runnerConfig.Command, "*") {
			return fmt.Errorf("command field must not start with '*': %s", runnerConfig.Command)
		}
		return nil
	}
	if strings.TrimSpace(runnerConfig.URL) == "" {
		return errors.New("url is required for http runner")
	}
	if executorConfig.StdinIdleTimeout > 0 {
		return errors.New("stdin_idle_timeout is not supported for http runner")
	}
	return nil
}

func validateExecutorDurations(config cmd.ExecutorConfig) error {
	if config.Timeout < 0 {
		return errors.New("timeout must be >= 0")
	}
	if config.Timeout > 0 && config.Timeout < time.Millisecond {
		return errors.New("timeout must be 0 or at least 1ms")
	}
	if config.StdinIdleTimeout < 0 {
		return errors.New("stdin_idle_timeout must be >= 0")
	}
	if config.StdinIdleTimeout > 0 && config.StdinIdleTimeout < time.Millisecond {
		return errors.New("stdin_idle_timeout must be 0 or at least 1ms")
	}
	return nil
}

func validateKeywordWildcards(keyword string) error {
	wildcards := 0
	for _, token := range strings.Fields(keyword) {
		if token == "*" {
			wildcards++
		}
	}
	if wildcards > 1 {
		return errors.New("keyword must not contain more than one wildcard")
	}
	return nil
}

func validateTTY(c cmd.ExecutorConfig) error {
	if c.TTY && c.StdinIdleTimeout > 0 {
		return errors.New("tty cannot be used with stdin_idle_timeout")
	}
	return nil
}

func normalizeRunner(c *cmd.RunnerConfig) (string, error) {
	runner := strings.ToLower(strings.TrimSpace(c.Runner))
	if runner == "" {
		runner = cmd.RunnerExec
	}
	switch runner {
	case cmd.RunnerExec, cmd.RunnerCompose, cmd.RunnerHTTP:
		c.Runner = runner
	default:
		return "", fmt.Errorf("unknown runner %q; valid values are %q, %q, and %q", c.Runner, cmd.RunnerExec, cmd.RunnerCompose, cmd.RunnerHTTP)
	}
	if runner == cmd.RunnerHTTP {
		c.Method = strings.ToUpper(strings.TrimSpace(c.Method))
		if c.Method == "" {
			c.Method = "POST"
		}
	}
	return runner, nil
}

func normalizeInteraction(value string) (string, error) {
	switch value {
	case "", cmd.InteractionOneshot:
		return cmd.InteractionOneshot, nil
	case cmd.InteractionStdin, cmd.InteractionCommand:
		return value, nil
	default:
		return "", fmt.Errorf("unknown interaction %q; valid values are %q, %q, and %q", value, cmd.InteractionOneshot, cmd.InteractionStdin, cmd.InteractionCommand)
	}
}

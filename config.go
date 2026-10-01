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
	commandConfigs      []*cmd.CommandConfig
}

type RawCommandConfig struct {
	cmd.MatcherConfig
	cmd.RunnerConfig
	RawExecutorConfig
	pubsub.ReplyConfig
	Interaction         string    `toml:"interaction"`
	OutputFlushInterval *Duration `toml:"output_flush_interval"`
	Replies             []*RawCommandConfig
}

// RawExecutorConfig preserves whether TOML execution settings were omitted.
type RawExecutorConfig struct {
	Timeout          *int  `toml:"timeout"`
	StdinIdleTimeout *int  `toml:"stdin_idle_timeout"`
	TTY              *bool `toml:"tty"`
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

func buildCommandSet(configs []*cmd.CommandConfig, factory cmd.RunnerFactory) *cmd.CommandSet {
	commands := make([]*cmd.Command, 0, len(configs))
	for _, config := range configs {
		commands = append(commands, buildCommand(config, factory))
	}
	return cmd.NewCommandSet(commands)
}

func buildCommand(config *cmd.CommandConfig, factory cmd.RunnerFactory) *cmd.Command {
	replies := buildCommandSet(config.Replies, factory)
	return cmd.NewCommand(*config, factory(config.RunnerConfig), replies)
}

func resolveOutputFlushInterval(value *Duration, inherited time.Duration) time.Duration {
	if value != nil {
		return time.Duration(*value)
	}
	return inherited
}

func resolveCommandConfig(raw *RawCommandConfig, inheritedOutputFlushInterval time.Duration) (*cmd.CommandConfig, error) {
	if validationErr := validateReplyConfig(&raw.ReplyConfig); validationErr != nil {
		return nil, fmt.Errorf("keyword '%s': %w", raw.Keyword, validationErr)
	}
	interaction, err := normalizeInteraction(raw.Interaction)
	if err != nil {
		return nil, fmt.Errorf("keyword '%s': %w", raw.Keyword, err)
	}
	runnerConfig := raw.RunnerConfig
	if _, err := normalizeRunner(&runnerConfig, raw.Keyword); err != nil {
		return nil, err
	}
	executorConfig, threadReplyMode := resolveInteraction(interaction, resolveExecutorConfig(cmd.ExecutorConfig{}, raw.RawExecutorConfig))
	outputFlushInterval := resolveOutputFlushInterval(raw.OutputFlushInterval, inheritedOutputFlushInterval)
	if err := validateCommandDefinition(raw.MatcherConfig, runnerConfig, executorConfig, outputFlushInterval); err != nil {
		return nil, err
	}
	if runnerConfig.Runner == cmd.RunnerHTTP && interaction == cmd.InteractionStdin {
		return nil, fmt.Errorf("http runner does not support stdin interaction for keyword '%s'", raw.Keyword)
	}
	config := &cmd.CommandConfig{MatcherConfig: raw.MatcherConfig, RunnerConfig: runnerConfig, ExecutorConfig: executorConfig, OutputFlushInterval: outputFlushInterval, ReplyConfig: &raw.ReplyConfig, SystemReplyConfig: pubsub.NewSystemReplyConfig(raw.ReplyBroadcast), ThreadReplyMode: threadReplyMode}
	for _, reply := range raw.Replies {
		resolved, err := resolveReplyCommandConfig(config, reply)
		if err != nil {
			return nil, err
		}
		config.Replies = append(config.Replies, resolved)
	}
	return config, nil
}

func resolveReplyCommandConfig(parent *cmd.CommandConfig, raw *RawCommandConfig) (*cmd.CommandConfig, error) {
	if raw.Interaction != "" {
		return nil, fmt.Errorf("keyword '%s': interaction is not allowed on reply commands", raw.Keyword)
	}
	if len(raw.Replies) > 0 {
		return nil, fmt.Errorf("keyword '%s': nested replies are not supported", raw.Keyword)
	}
	runnerConfig := raw.RunnerConfig
	if runnerConfig.Runner == "" {
		runnerConfig.Runner = parent.Runner
	}
	if _, err := normalizeRunner(&runnerConfig, raw.Keyword); err != nil {
		return nil, err
	}
	executorConfig := resolveExecutorConfig(parent.ExecutorConfig, raw.RawExecutorConfig)
	outputFlushInterval := resolveOutputFlushInterval(raw.OutputFlushInterval, parent.OutputFlushInterval)
	replyConfig, ok := parent.ReplyConfig.(*pubsub.ReplyConfig)
	if !ok {
		return nil, errors.New("resolved parent reply config has unexpected type")
	}
	resolvedReplyConfig := resolveReplyConfig(*replyConfig, raw)
	if err := validateReplyConfig(resolvedReplyConfig); err != nil {
		return nil, fmt.Errorf("keyword '%s': %w", raw.Keyword, err)
	}
	if err := validateCommandDefinition(raw.MatcherConfig, runnerConfig, executorConfig, outputFlushInterval); err != nil {
		return nil, err
	}
	return &cmd.CommandConfig{MatcherConfig: raw.MatcherConfig, RunnerConfig: runnerConfig, ExecutorConfig: executorConfig, OutputFlushInterval: outputFlushInterval, ReplyConfig: resolvedReplyConfig, SystemReplyConfig: pubsub.NewSystemReplyConfig(resolvedReplyConfig.ReplyBroadcast), ThreadReplyMode: cmd.ThreadReplyIgnore}, nil
}

func resolveExecutorConfig(base cmd.ExecutorConfig, raw RawExecutorConfig) cmd.ExecutorConfig {
	if raw.Timeout != nil {
		base.Timeout = *raw.Timeout
	}
	if raw.StdinIdleTimeout != nil {
		base.StdinIdleTimeout = *raw.StdinIdleTimeout
	}
	if raw.TTY != nil {
		base.TTY = *raw.TTY
	}
	return base
}

func resolveInteraction(interaction string, executorConfig cmd.ExecutorConfig) (cmd.ExecutorConfig, cmd.ThreadReplyMode) {
	switch interaction {
	case cmd.InteractionStdin:
		executorConfig.AllowInChain = false
		executorConfig.InteractiveStdin = true
		executorConfig.InputBodyMode = cmd.InputBodyStdin
		return executorConfig, cmd.ThreadReplyStdin
	case cmd.InteractionCommand:
		executorConfig.AllowInChain = false
		executorConfig.InteractiveStdin = false
		executorConfig.InputBodyMode = cmd.InputBodyArgument
		return executorConfig, cmd.ThreadReplyCommand
	default:
		executorConfig.AllowInChain = true
		executorConfig.InteractiveStdin = false
		executorConfig.InputBodyMode = cmd.InputBodyStdin
		return executorConfig, cmd.ThreadReplyIgnore
	}
}

func resolveConfig(cfg *Config) error {
	if strings.TrimSpace(cfg.SlackBotToken) == "" {
		return errors.New("slack_bot_token is required")
	}
	if strings.TrimSpace(cfg.SlackAppToken) == "" {
		return errors.New("slack_app_token is required")
	}
	if cfg.NumWorkers < 1 {
		return fmt.Errorf("num_workers must be >= 1 (got %d)", cfg.NumWorkers)
	}
	if err := validateOpenAccess(cfg); err != nil {
		return err
	}
	if err := validateReplyConfig(&cfg.ReplyConfig); err != nil {
		return err
	}
	outputFlushInterval := resolveOutputFlushInterval(
		cfg.OutputFlushInterval,
		cmd.DefaultOutputFlushInterval,
	)
	if outputFlushInterval < 0 {
		return errors.New("output_flush_interval must be >= 0")
	}

	cfg.commandConfigs = make([]*cmd.CommandConfig, 0, len(cfg.Commands))
	for _, c := range cfg.Commands {
		resolved, err := resolveCommandConfig(c, outputFlushInterval)
		if err != nil {
			return err
		}
		cfg.commandConfigs = append(cfg.commandConfigs, resolved)
	}
	return nil
}

func validateOpenAccess(cfg *Config) error {
	if len(cfg.AllowedUserIDs) == 0 &&
		len(cfg.AllowedChannelIDs) == 0 &&
		!cfg.AllowUnsafeOpenAccess {
		return errors.New(
			"open access is disabled by default: set allowed_user_ids and/or allowed_channel_ids, " +
				"or set allow_unsafe_open_access=true to keep old behavior",
		)
	}
	return nil
}

func validateReplyConfig(cfg *pubsub.ReplyConfig) error {
	switch cfg.OutputFormat {
	case "", pubsub.OutputFormatPlain, pubsub.OutputFormatMonospaced, pubsub.OutputFormatMarkdown:
		return nil
	default:
		return fmt.Errorf("unknown output_format %q", cfg.OutputFormat)
	}
}

func validateCommandDefinition(matcher cmd.MatcherConfig, runnerConfig cmd.RunnerConfig, executorConfig cmd.ExecutorConfig, outputFlushInterval time.Duration) error {
	if strings.TrimSpace(matcher.Keyword) == "" {
		return errors.New("keyword is required")
	}
	if err := validateKeywordWildcards(matcher.Keyword); err != nil {
		return err
	}
	if executorConfig.Timeout < 0 {
		return fmt.Errorf("timeout must be >= 0 for keyword %q", matcher.Keyword)
	}
	if executorConfig.StdinIdleTimeout < 0 {
		return fmt.Errorf("stdin_idle_timeout must be >= 0 for keyword '%s'", matcher.Keyword)
	}
	if outputFlushInterval < 0 {
		return fmt.Errorf("output_flush_interval must be >= 0 for keyword %q", matcher.Keyword)
	}
	runner := runnerConfig.Runner
	if err := validateTTY(executorConfig, matcher.Keyword); err != nil {
		return err
	}
	if executorConfig.TTY && runner == cmd.RunnerHTTP {
		return fmt.Errorf("tty is not supported for http runner (keyword '%s')", matcher.Keyword)
	}
	if runner != cmd.RunnerHTTP {
		if strings.TrimSpace(runnerConfig.Command) == "" {
			return fmt.Errorf("command is required for keyword %q", matcher.Keyword)
		}
		if strings.HasPrefix(runnerConfig.Command, "*") {
			return fmt.Errorf("command field must not start with '*': %s", runnerConfig.Command)
		}
		return nil
	}
	if strings.TrimSpace(runnerConfig.URL) == "" {
		return fmt.Errorf("url is required for http runner (keyword '%s')", matcher.Keyword)
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
		return fmt.Errorf("keyword %q must not contain more than one wildcard", keyword)
	}
	return nil
}

func validateTTY(c cmd.ExecutorConfig, keyword string) error {
	if c.TTY && c.StdinIdleTimeout > 0 {
		return fmt.Errorf(
			"tty cannot be used with stdin_idle_timeout for keyword '%s'",
			keyword,
		)
	}
	return nil
}

func normalizeRunner(c *cmd.RunnerConfig, keyword string) (string, error) {
	runner := strings.ToLower(strings.TrimSpace(c.Runner))
	if runner == "" {
		runner = cmd.RunnerExec
	}
	switch runner {
	case cmd.RunnerExec, cmd.RunnerCompose, cmd.RunnerHTTP:
		c.Runner = runner
	default:
		return "", fmt.Errorf("unknown runner '%s' for keyword '%s'", c.Runner, keyword)
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
		return "", fmt.Errorf("unknown interaction %q", value)
	}
}

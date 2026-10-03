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
	matcherConfig := cmd.MatcherConfig{RawMatcherConfig: raw.RawMatcherConfig}
	runnerConfig := cmd.RunnerConfig{RawRunnerConfig: raw.RawRunnerConfig}
	if _, err := normalizeRunner(&runnerConfig, raw.Keyword); err != nil {
		return nil, err
	}
	parserConfig, executorConfig := resolveInteraction(interaction, resolveExecutorConfig(cmd.ExecutorConfig{}, raw.RawExecutorConfig))
	outputFlushInterval := resolveOutputFlushInterval(raw.OutputFlushInterval, inheritedOutputFlushInterval)
	if err := validateCommandDefinition(matcherConfig, runnerConfig, executorConfig, outputFlushInterval); err != nil {
		return nil, err
	}
	if runnerConfig.Runner == cmd.RunnerHTTP && interaction == cmd.InteractionStdin {
		return nil, fmt.Errorf("http runner does not support stdin interaction for keyword '%s'", raw.Keyword)
	}
	config := &cmd.CommandConfig{MatcherConfig: matcherConfig, ParserConfig: parserConfig, RunnerConfig: runnerConfig, ExecutorConfig: executorConfig, OutputFlushInterval: outputFlushInterval, ReplyConfig: &raw.ReplyConfig, SystemReplyConfig: pubsub.NewSystemReplyConfig(raw.ReplyBroadcast), Dispatch: commandDispatch(runnerConfig.Runner)}
	configuredReplies := make([]*cmd.CommandConfig, 0, len(raw.Replies))
	for _, reply := range raw.Replies {
		resolved, err := resolveReplyCommandConfig(config, reply)
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
		config.Replies = []*cmd.CommandConfig{{
			MatcherConfig:  cmd.MatcherConfig{RawMatcherConfig: cmd.RawMatcherConfig{Keyword: "*"}},
			ParserConfig:   replyParserConfig,
			RunnerConfig:   cmd.RunnerConfig{RawRunnerConfig: cmd.RawRunnerConfig{Runner: cmd.RunnerStdinReply, Command: "stdin-reply"}},
			ExecutorConfig: executorConfig,
			Dispatch:       cmd.CommandDispatchRunner,
		}}
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
	matcherConfig := cmd.MatcherConfig{RawMatcherConfig: raw.RawMatcherConfig}
	runnerConfig := cmd.RunnerConfig{RawRunnerConfig: raw.RawRunnerConfig}
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
	if err := validateCommandDefinition(matcherConfig, runnerConfig, executorConfig, outputFlushInterval); err != nil {
		return nil, err
	}
	return &cmd.CommandConfig{MatcherConfig: matcherConfig, ParserConfig: parent.ParserConfig, RunnerConfig: runnerConfig, ExecutorConfig: executorConfig, OutputFlushInterval: outputFlushInterval, ReplyConfig: resolvedReplyConfig, SystemReplyConfig: pubsub.NewSystemReplyConfig(resolvedReplyConfig.ReplyBroadcast), Dispatch: commandDispatch(runnerConfig.Runner)}, nil
}

func commandDispatch(runner string) cmd.CommandDispatch {
	if runner == cmd.RunnerHTTP {
		return cmd.CommandDispatchExecutor
	}
	return cmd.CommandDispatchQueue
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

func resolveInteraction(interaction string, executorConfig cmd.ExecutorConfig) (cmd.ParserConfig, cmd.ExecutorConfig) {
	parserConfig := cmd.ParserConfig{}
	switch interaction {
	case cmd.InteractionStdin:
		parserConfig.AllowInChain = false
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
	if strings.TrimSpace(cfg.SlackBotToken) == "" {
		return errors.New("slack_bot_token is required")
	}
	if strings.TrimSpace(cfg.SlackAppToken) == "" {
		return errors.New("slack_app_token is required")
	}
	if cfg.NumWorkers < 1 {
		return fmt.Errorf("num_workers must be >= 1 (got %d)", cfg.NumWorkers)
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
	cfg.ListenerConfigs = make([]pubsub.ListenerConfig, 0)
	listenerDefaults := pubsub.RawListenerConfig{AllowedUserIDs: cfg.AllowedUserIDs, AllowedChannelIDs: cfg.AllowedChannelIDs}
	nextCommandIndex := 0
	for _, c := range cfg.Commands {
		resolved, err := resolveCommandConfig(c, outputFlushInterval)
		if err != nil {
			return err
		}
		nextCommandIndex, err = assignCommandIndexes(c, resolved, listenerDefaults, nextCommandIndex, &cfg.ListenerConfigs)
		if err != nil {
			return err
		}
		cfg.commandConfigs = append(cfg.commandConfigs, resolved)
	}
	if err := validateOpenAccess(cfg); err != nil {
		return err
	}
	return nil
}

func assignCommandIndexes(raw *RawCommandConfig, resolved *cmd.CommandConfig, inherited pubsub.RawListenerConfig, next int, flat *[]pubsub.ListenerConfig) (int, error) {
	resolved.Index = next
	listenerConfig, err := resolveListenerConfig(raw.RawListenerConfig, inherited, raw.Keyword)
	if err != nil {
		return next, err
	}
	*flat = append(*flat, pubsub.ListenerConfig{CommandIndex: next, RawListenerConfig: listenerConfig})
	next++

	resolvedReplyACLs := make([]pubsub.RawListenerConfig, len(raw.Replies))
	for i, rawReply := range raw.Replies {
		resolvedReplyACLs[i], err = resolveListenerConfig(rawReply.RawListenerConfig, listenerConfig, rawReply.Keyword)
		if err != nil {
			return next, err
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

func resolveListenerConfig(raw, inherited pubsub.RawListenerConfig, keyword string) (pubsub.RawListenerConfig, error) {
	resolved := pubsub.RawListenerConfig{AcceptReminder: raw.AcceptReminder}
	var err error
	resolved.AllowedUserIDs, err = resolveAllowedIDs(raw.AllowedUserIDs, inherited.AllowedUserIDs, keyword, "allowed_user_ids")
	if err != nil {
		return pubsub.RawListenerConfig{}, err
	}
	resolved.AllowedChannelIDs, err = resolveAllowedIDs(raw.AllowedChannelIDs, inherited.AllowedChannelIDs, keyword, "allowed_channel_ids")
	if err != nil {
		return pubsub.RawListenerConfig{}, err
	}
	return resolved, nil
}

func resolveAllowedIDs(value []string, inherited []string, keyword, name string) ([]string, error) {
	if len(value) == 0 {
		return inherited, nil
	}
	resolved := append([]string(nil), value...)
	if len(inherited) == 0 {
		return resolved, nil
	}
	for _, id := range resolved {
		if !containsString(inherited, id) {
			return nil, fmt.Errorf("keyword '%s': %s value %q is not allowed by its parent", keyword, name, id)
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

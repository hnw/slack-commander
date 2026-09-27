package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/pelletier/go-toml/v2"

	"github.com/hnw/slack-commander/cmd"
	"github.com/hnw/slack-commander/pubsub"
)

type PubSubConfig = pubsub.Config // TOMLデコード対象のためexportedにする

type Config struct {
	PubSubConfig
	NumWorkers int `toml:"num_workers"`
	Commands   []*CommandConfig
}

type CommandConfig struct {
	cmd.Definition
	pubsub.ReplyConfig
	Interaction string `toml:"interaction"`
	Replies     []*ReplyCommandConfig
}

// ReplyCommandConfig is a thread-reply command definition.
// It deliberately has no Replies field: nested reply routing is unsupported.
type ReplyCommandConfig struct {
	cmd.Definition
	pubsub.ReplyConfig
	Runner           string `toml:"runner"`
	Timeout          *int   `toml:"timeout"`
	StdinIdleTimeout *int   `toml:"stdin_idle_timeout"`
	TTY              *bool  `toml:"tty"`
	Username         string `toml:"username"`
	IconEmoji        string `toml:"icon_emoji"`
	IconURL          string `toml:"icon_url"`
	ReplyBroadcast   *bool  `toml:"reply_broadcast"`
	OutputFormat     string `toml:"output_format"`
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
	if err := validateConfig(cfg); err != nil {
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

func resolveReplyCommand(
	parent *CommandConfig,
	reply *ReplyCommandConfig,
) (*cmd.Definition, *pubsub.ReplyConfig) {
	definition := reply.Definition
	if reply.Runner == "" {
		definition.Runner = parent.Runner
	} else {
		definition.Runner = reply.Runner
	}
	if reply.Timeout == nil {
		definition.Timeout = parent.Timeout
	} else {
		definition.Timeout = *reply.Timeout
	}
	if reply.StdinIdleTimeout == nil {
		definition.StdinIdleTimeout = parent.StdinIdleTimeout
	} else {
		definition.StdinIdleTimeout = *reply.StdinIdleTimeout
	}
	if reply.TTY == nil {
		definition.TTY = parent.TTY
	} else {
		definition.TTY = *reply.TTY
	}

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
	return &definition, replyConfig
}

func commandConfigs(configs []*CommandConfig) []*cmd.CommandConfig {
	converted := make([]*cmd.CommandConfig, len(configs))
	for i, config := range configs {
		converted[i] = cmd.NewCommandConfig(&config.Definition, &config.ReplyConfig)
		converted[i].SystemReplyConfig = pubsub.NewSystemReplyConfig(config.ReplyBroadcast)
		converted[i].Interaction = config.Interaction
		converted[i].Replies = make([]*cmd.CommandConfig, len(config.Replies))
		for j, reply := range config.Replies {
			converted[i].Replies[j] = cmd.NewCommandConfig(&reply.Definition, &reply.ReplyConfig)
			converted[i].Replies[j].SystemReplyConfig = pubsub.NewSystemReplyConfig(
				reply.ReplyConfig.ReplyBroadcast,
			)
		}
	}
	return converted
}

func validateConfig(cfg *Config) error {
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

	for _, c := range cfg.Commands {
		if err := validateReplyConfig(&c.ReplyConfig); err != nil {
			return fmt.Errorf("keyword '%s': %w", c.Keyword, err)
		}
		interaction, err := normalizeInteraction(c.Interaction)
		if err != nil {
			return fmt.Errorf("keyword '%s': %w", c.Keyword, err)
		}
		c.Interaction = interaction
		if err := validateCommandDefinition(&c.Definition); err != nil {
			return err
		}
		if strings.EqualFold(strings.TrimSpace(c.Runner), cmd.RunnerHTTP) && c.Interaction == cmd.InteractionStdin {
			return fmt.Errorf("http runner does not support stdin interaction for keyword '%s'", c.Keyword)
		}
		for _, reply := range c.Replies {
			definition, replyConfig := resolveReplyCommand(c, reply)
			if err := validateReplyConfig(replyConfig); err != nil {
				return fmt.Errorf("keyword '%s': %w", reply.Keyword, err)
			}
			if err := validateCommandDefinition(definition); err != nil {
				return err
			}
			reply.Definition = *definition
			reply.ReplyConfig = *replyConfig
		}
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

func validateCommandDefinition(c *cmd.Definition) error {
	if strings.TrimSpace(c.Keyword) == "" {
		return errors.New("keyword is required")
	}
	if err := validateKeywordWildcards(c.Keyword); err != nil {
		return err
	}
	if c.Timeout < 0 {
		return fmt.Errorf("timeout must be >= 0 for keyword %q", c.Keyword)
	}
	if c.StdinIdleTimeout < 0 {
		return fmt.Errorf("stdin_idle_timeout must be >= 0 for keyword '%s'", c.Keyword)
	}
	runner, err := normalizeRunner(c)
	if err != nil {
		return err
	}
	if err := validateTTY(c); err != nil {
		return err
	}
	if runner != cmd.RunnerHTTP {
		if strings.TrimSpace(c.Command) == "" {
			return fmt.Errorf("command is required for keyword %q", c.Keyword)
		}
		if strings.HasPrefix(c.Command, "*") {
			return fmt.Errorf("command field must not start with '*': %s", c.Command)
		}
		return nil
	}
	c.Method = strings.ToUpper(strings.TrimSpace(c.Method))
	if c.Method == "" {
		c.Method = "POST"
	}
	if strings.TrimSpace(c.URL) == "" {
		return fmt.Errorf("url is required for http runner (keyword '%s')", c.Keyword)
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

func validateTTY(c *cmd.Definition) error {
	if c.TTY && c.StdinIdleTimeout > 0 {
		return fmt.Errorf(
			"tty cannot be used with stdin_idle_timeout for keyword '%s'",
			c.Keyword,
		)
	}
	return nil
}

func normalizeRunner(c *cmd.Definition) (string, error) {
	runner := strings.ToLower(strings.TrimSpace(c.Runner))
	if runner == "" {
		runner = cmd.RunnerExec
	}
	switch runner {
	case cmd.RunnerExec, cmd.RunnerCompose, cmd.RunnerHTTP:
		c.Runner = runner
	default:
		return "", fmt.Errorf("unknown runner '%s' for keyword '%s'", c.Runner, c.Keyword)
	}
	if c.TTY && runner == cmd.RunnerHTTP {
		return "", fmt.Errorf("tty is not supported for http runner (keyword '%s')", c.Keyword)
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

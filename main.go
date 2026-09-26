// Package main is the entry point for slack-commander.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	"github.com/BurntSushi/toml"
	"github.com/slack-go/slack"
	"github.com/slack-go/slack/socketmode"

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
	Interaction cmd.Interaction `toml:"interaction"`
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

func main() {
	var (
		quiet      = flag.Bool("q", false, "Quiet mode")
		configFile = flag.String("config-file", "config.toml", "Specify configuration file")
		verbose    = flag.Bool("v", false, "Verbose mode")
		debug      = flag.Bool("debug", false, "Debug mode") // slack-go/slackのdebug mode
	)
	flag.Parse()

	zapCfg := zap.NewDevelopmentConfig()
	zapCfg.DisableStacktrace = true
	zapCfg.EncoderConfig.EncodeTime = zapcore.RFC3339NanoTimeEncoder
	zapCfg.EncoderConfig.EncodeDuration = zapcore.SecondsDurationEncoder
	zapCfg.Level.SetLevel(zapcore.WarnLevel)
	if *verbose {
		zapCfg.Level.SetLevel(zapcore.InfoLevel)
	}
	if *debug {
		zapCfg.Level.SetLevel(zapcore.DebugLevel)
	}
	if *quiet {
		zapCfg.Level.SetLevel(zapcore.ErrorLevel)
	}

	logger, err := zapCfg.Build()
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v", err)
		return
	}
	defer func() {
		_ = logger.Sync()
	}()
	sugar := logger.Sugar()
	stdLogger, err := zap.NewStdLogAt(logger, zapcore.DebugLevel)
	if err != nil {
		sugar.Errorf("%v", err)
		return
	}
	cfg := Config{NumWorkers: 1}
	metadata, err := toml.DecodeFile(*configFile, &cfg)
	if err != nil {
		sugar.Errorf("%v", err)
		return
	}
	if err := validateTOMLMetadata(metadata); err != nil {
		sugar.Fatalf("Fatal: %v", err)
	}
	if err := validateConfig(&cfg); err != nil {
		sugar.Fatalf("Fatal: %v", err)
	}

	cmdConfig := commandConfigs(cfg.Commands)

	api := slack.New(
		cfg.SlackBotToken,
		slack.OptionDebug(*debug),
		slack.OptionLog(stdLogger),
		slack.OptionAppLevelToken(cfg.SlackAppToken),
	)
	smc := socketmode.New(
		api,
		socketmode.OptionDebug(*debug),
		socketmode.OptionLog(stdLogger),
	)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// チャンネルの容量を大きめに取る。本来cfg.NumWorkersで問題ないはずだが、
	// ack返せない問題への暫定対処。
	commandQueue := make(chan *cmd.CommandInput, 50)
	outputQueue := make(chan *cmd.CommandOutput, cfg.NumWorkers)
	var threadInputs cmd.ThreadInputRegistry
	threadRoutes := cmd.NewThreadRouteCache(4096)
	var threadLocks cmd.ThreadLocks
	var composeRunnerOnce sync.Once
	var composeRunner cmd.CommandRunner
	runnerFactory := func(cfg *cmd.CommandConfig) cmd.CommandRunner {
		if cfg.Runner == "compose" {
			composeRunnerOnce.Do(func() {
				composeRunner = cmd.NewComposeRunner("")
			})
			return composeRunner
		}
		if cfg.Runner == "http" {
			return cmd.NewHTTPRunner(cfg)
		}
		return cmd.NewExecRunner()
	}
	var executorWG sync.WaitGroup
	for i := 0; i < cfg.NumWorkers; i++ {
		executorWG.Add(1)
		go func() {
			defer executorWG.Done()
			cmd.ExecutorWithThreadInputAndLocks(
				ctx,
				commandQueue,
				outputQueue,
				cmdConfig,
				runnerFactory,
				&threadInputs,
				&threadLocks,
			)
		}()
	}
	var writerWG sync.WaitGroup
	writerWG.Add(1)
	go func() {
		defer writerWG.Done()
		pubsub.SlackWriter(ctx, smc, outputQueue)
	}()
	var listenerWG sync.WaitGroup
	listenerWG.Add(1)
	go func() {
		defer listenerWG.Done()
		pubsub.SlackListener(
			ctx,
			smc,
			commandQueue,
			cfg.PubSubConfig,
			&threadInputs,
			cmdConfig,
			threadRoutes,
		)
	}()

	if err := smc.RunContext(ctx); err != nil && !errors.Is(err, context.Canceled) {
		sugar.Errorf("Socket Mode error: %v", err)
	}
	stop()
	listenerWG.Wait()
	close(commandQueue)
	executorWG.Wait()
	close(outputQueue)
	writerWG.Wait()
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
	if cfg.NumWorkers < 1 {
		return fmt.Errorf("num_workers must be >= 1 (got %d)", cfg.NumWorkers)
	}
	if len(cfg.AllowedUserIDs) == 0 &&
		len(cfg.AllowedChannelIDs) == 0 &&
		!cfg.AllowUnsafeOpenAccess {
		return errors.New(
			"open access is disabled by default: set allowed_user_ids and/or allowed_channel_ids, " +
				"or set allow_unsafe_open_access=true to keep old behavior",
		)
	}
	if err := validateReplyConfig(&cfg.ReplyConfig); err != nil {
		return err
	}

	for _, c := range cfg.Commands {
		if err := validateReplyConfig(&c.ReplyConfig); err != nil {
			return fmt.Errorf("keyword '%s': %w", c.Keyword, err)
		}
		interaction, err := c.Interaction.Normalize()
		if err != nil {
			return fmt.Errorf("keyword '%s': %w", c.Keyword, err)
		}
		c.Interaction = interaction
		if err := validateCommandDefinition(&c.Definition); err != nil {
			return err
		}
		if strings.EqualFold(strings.TrimSpace(c.Runner), "http") && c.Interaction != cmd.InteractionOneshot {
			return fmt.Errorf("http runner only supports oneshot interaction for keyword '%s'", c.Keyword)
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

func validateReplyConfig(cfg *pubsub.ReplyConfig) error {
	switch cfg.OutputFormat {
	case "", "plain", "monospaced", "markdown":
		return nil
	default:
		return fmt.Errorf("unknown output_format %q", cfg.OutputFormat)
	}
}

func validateCommandDefinition(c *cmd.Definition) error {
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
	if runner != "http" {
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
		runner = "exec"
	}
	switch runner {
	case "exec", "compose", "http":
		c.Runner = runner
	default:
		return "", fmt.Errorf("unknown runner '%s' for keyword '%s'", c.Runner, c.Keyword)
	}
	if c.TTY && runner == "http" {
		return "", fmt.Errorf("tty is not supported for http runner (keyword '%s')", c.Keyword)
	}
	return runner, nil
}

func validateTOMLMetadata(metadata toml.MetaData) error {
	for _, key := range metadata.Undecoded() {
		if len(key) >= 3 && key[0] == "commands" && key[1] == "replies" && key[2] == "replies" {
			return errors.New("commands.replies.replies is not supported")
		}
		if len(key) == 3 && key[0] == "commands" && key[1] == "replies" && key[2] == "interaction" {
			return errors.New("commands.replies.interaction is not supported")
		}
	}
	return nil
}

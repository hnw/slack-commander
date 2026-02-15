package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"sync"

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
	defer logger.Sync()
	sugar := logger.Sugar()
	stdLogger, err := zap.NewStdLogAt(logger, zapcore.DebugLevel)
	if err != nil {
		sugar.Errorf("%v", err)
		return
	}
	cfg := Config{NumWorkers: 1}
	if _, err := toml.DecodeFile(*configFile, &cfg); err != nil {
		sugar.Errorf("%v", err)
		return
	}
	if err := validateConfig(&cfg); err != nil {
		sugar.Fatalf("Fatal: %v", err)
	}

	// 構造体の詰め替え（TOMLライブラリの都合とパッケージ分割の都合）
	cmdConfig := make([]*cmd.CommandConfig, len(cfg.Commands))
	for i, c := range cfg.Commands {
		cmdConfig[i] = cmd.NewCommandConfig(&c.Definition, &c.ReplyConfig)
	}

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

	commandQueue := make(chan *cmd.CommandInput, 50) // チャンネルの容量を大きめに取る。本来cfg.NumWorkersで問題ないはずだが、ack返せない問題への暫定対処
	outputQueue := make(chan *cmd.CommandOutput, cfg.NumWorkers)
	var composeRunnerOnce sync.Once
	var composeRunner cmd.CommandRunner
	runnerFactory := func(cfg *cmd.CommandConfig) cmd.CommandRunner {
		if cfg.Runner == "compose" {
			composeRunnerOnce.Do(func() {
				composeRunner = cmd.NewComposeRunner("")
			})
			return composeRunner
		}
		return cmd.NewExecRunner()
	}
	for i := 0; i < cfg.NumWorkers; i++ {
		go cmd.ExecutorWithRunner(commandQueue, outputQueue, cmdConfig, runnerFactory)
	}
	go pubsub.SlackWriter(smc, outputQueue)
	go pubsub.SlackListener(smc, commandQueue, cfg.PubSubConfig)

	smc.Run()
}

func validateConfig(cfg *Config) error {
	if cfg.NumWorkers < 1 {
		return fmt.Errorf("num_workers must be >= 1 (got %d)", cfg.NumWorkers)
	}
	if len(cfg.AllowedUserIDs) == 0 && len(cfg.AllowedChannelIDs) == 0 && !cfg.AllowUnsafeOpenAccess {
		return errors.New("open access is disabled by default: set allowed_user_ids and/or allowed_channel_ids, or set allow_unsafe_open_access=true to keep old behavior")
	}

	for _, c := range cfg.Commands {
		if strings.HasPrefix(c.Command, "*") {
			return fmt.Errorf("command field must not start with '*': %s", c.Command)
		}
		runner := strings.ToLower(strings.TrimSpace(c.Runner))
		if runner == "" {
			runner = "exec"
		}
		switch runner {
		case "exec", "compose":
			c.Runner = runner
		default:
			return fmt.Errorf("unknown runner '%s' for keyword '%s'", c.Runner, c.Keyword)
		}
	}
	return nil
}

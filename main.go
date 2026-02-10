package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

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

var (
	cfg Config
)

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
	if cfg.NumWorkers < 1 {
		sugar.Fatalf("Fatal: num_workers must be >= 1 (got %d)", cfg.NumWorkers)
	}

	// Validate configuration
	for _, c := range cfg.Commands {
		if strings.HasPrefix(c.Command, "*") {
			sugar.Fatalf("Fatal: Command field must not start with '*': %s", c.Command)
		}
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
	for i := 0; i < cfg.NumWorkers; i++ {
		go cmd.Executor(commandQueue, outputQueue, cmdConfig)
	}
	go pubsub.SlackWriter(smc, outputQueue)
	go pubsub.SlackListener(smc, commandQueue, cfg.PubSubConfig)

	smc.Run()
}

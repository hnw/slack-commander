package main

import (
	"flag"
	"fmt"
	"os"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	"github.com/BurntSushi/toml"
	"github.com/slack-go/slack"
	"github.com/slack-go/slack/socketmode"

	"github.com/hnw/slack-commander/cmd"
	"github.com/hnw/slack-commander/pubsub"
)

type pubSubConfig = pubsub.Config // 名前が重複しているためaliasにして埋め込み

type Config struct {
	pubSubConfig
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
		quiet   = flag.Bool("q", false, "Quiet mode")
		verbose = flag.Bool("v", false, "Verbose mode")
		debug   = flag.Bool("debug", false, "Debug mode") // slack-go/slackのdebug mode
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
	cfg = Config{NumWorkers: 1}
	if _, err := toml.DecodeFile("config.toml", &cfg); err != nil {
		sugar.Errorf("%v", err)
		return
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
	go pubsub.SlackListener(smc, commandQueue, cfg.pubSubConfig)

	smc.Run()
}

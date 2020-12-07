package main

import (
	"flag"
	"log"
	"os"

	"github.com/BurntSushi/toml"
	"github.com/hashicorp/logutils"
	"github.com/slack-go/slack"

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
	cfg    Config
	logger *log.Logger
)

func main() {
	var (
		verbose = flag.Bool("v", false, "Verbose mode")
	)
	flag.Parse()

	logLevel := "INFO"
	if *verbose {
		logLevel = "DEBUG"
	}

	filter := &logutils.LevelFilter{
		Levels:   []logutils.LogLevel{"DEBUG", "INFO", "ERROR"},
		MinLevel: logutils.LogLevel(logLevel),
		Writer:   os.Stderr,
	}
	logger = log.New(os.Stderr, "", log.Lshortfile|log.LstdFlags)
	logger.SetOutput(filter)

	cfg = Config{NumWorkers: 1}
	if _, err := toml.DecodeFile("config.toml", &cfg); err != nil {
		logger.Println("[ERROR] ", err)
		return
	}
	// 構造体の詰め替え（TOMLライブラリの都合とパッケージ分割の都合）
	cmdConfig := make([]*cmd.CommandConfig, len(cfg.Commands))
	for i, c := range cfg.Commands {
		cmdConfig[i] = cmd.NewCommandConfig(&c.Definition, &c.ReplyConfig)
	}

	optionLogger := slack.OptionLog(logger)
	optionDebug := slack.OptionDebug(*verbose)
	api := slack.New(cfg.SlackToken, optionLogger, optionDebug)

	rtm := api.NewRTM()
	go rtm.ManageConnection()
	commandQueue := make(chan *cmd.CommandInput, cfg.NumWorkers)
	outputQueue := make(chan *cmd.CommandOutput, cfg.NumWorkers)
	for i := 0; i < cfg.NumWorkers; i++ {
		go cmd.Executor(commandQueue, outputQueue, cmdConfig)
	}
	go pubsub.SlackWriter(rtm, outputQueue)
	pubsub.SlackListener(rtm, commandQueue, cfg.pubSubConfig)
}

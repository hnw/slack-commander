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

type pubSub = pubsub.Config

type Config struct {
	pubSub
	NumWorkers int `toml:"num_workers"`
	Commands   []*cmd.CommandConfig
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
	optionLogger := slack.OptionLog(logger)
	optionDebug := slack.OptionDebug(*verbose)
	api := slack.New(cfg.SlackToken, optionLogger, optionDebug)

	rtm := api.NewRTM()
	go rtm.ManageConnection()
	commandQueue := make(chan *pubsub.Input, cfg.NumWorkers)
	outputQueue := make(chan *pubsub.CommandOutput, cfg.NumWorkers)
	for i := 0; i < cfg.NumWorkers; i++ {
		go cmd.Executor(commandQueue, outputQueue, cfg.Commands)
	}
	go pubsub.SlackWriter(rtm, outputQueue)
	pubsub.SlackListener(rtm, commandQueue, cfg.pubSub)
}

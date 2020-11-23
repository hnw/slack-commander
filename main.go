package main

import (
	"flag"
	"io"
	"log"
	"os"

	"github.com/BurntSushi/toml"
	"github.com/hashicorp/logutils"
	"github.com/slack-go/slack"

	"github.com/hnw/slack-commander/pubsub"
)

type topLevelConfig struct {
	pubsub.TopLevelConfig
	NumWorkers int `toml:"num_workers"`
	Commands   []*commandConfig
}

type commandConfig struct {
	pubsub.Config
	Keyword string
	Command string
	Aliases []string
}

type commandOption struct {
	args        []string
	stdin       io.Reader
	stdout      io.Writer
	stderr      io.Writer
	cleanupFunc func()
	timeout     int
}

type commandOutput struct {
	commandConfig
	origMessage *slack.MessageEvent
	text        string
	isError     bool
}

type slackInput struct {
	message     *slack.MessageEvent // 起動メッセージ
	messageText string              // 起動コマンド平文
}

var (
	config topLevelConfig
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

	config = topLevelConfig{NumWorkers: 1}
	if _, err := toml.DecodeFile("config.toml", &config); err != nil {
		logger.Println("[ERROR] ", err)
		return
	}
	optionLogger := slack.OptionLog(logger)
	optionDebug := slack.OptionDebug(*verbose)
	api := slack.New(config.SlackToken, optionLogger, optionDebug)

	rtm := api.NewRTM()
	go rtm.ManageConnection()
	commandQueue := make(chan *pubsub.Input, config.NumWorkers)
	outputQueue := make(chan *pubsub.CommandOutput, config.NumWorkers)
	for i := 0; i < config.NumWorkers; i++ {
		go commandExecutor(commandQueue, outputQueue, config.Commands)
	}
	go pubsub.SlackWriter(rtm, outputQueue)
	pubsub.SlackListener(rtm, commandQueue, config.TopLevelConfig)
}

package main

import (
	"flag"
	"io"
	"log"
	"os"

	"github.com/BurntSushi/toml"
	"github.com/hashicorp/logutils"
	"github.com/slack-go/slack"
)

type commonConfig struct {
	Username        string `toml:"username"`
	IconEmoji       string `toml:"icon_emoji"`
	IconURL         string `toml:"icon_url"`
	PostAsReply     bool   `toml:"post_as_reply"`
	AlwaysBroadcast bool   `toml:"always_broadcast"`
	Monospaced      bool
	Timeout         int
}

type topLevelConfig struct {
	commonConfig
	NumWorkers          int    `toml:"num_workers"`
	SlackToken          string `toml:"slack_token"`
	AcceptReminder      bool   `toml:"accept_reminder"`
	AcceptBotMessage    bool   `toml:"accept_bot_message"`
	AcceptThreadMessage bool   `toml:"accept_thread_message"`
	Commands            []*commandConfig
}

type commandConfig struct {
	commonConfig
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

func newSlackInput(message *slack.MessageEvent, messageText string) *slackInput {
	i := slackInput{}
	i.message = message
	i.messageText = messageText
	return &i
}

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
	commandQueue := make(chan *slackInput, config.NumWorkers)
	writeQueue := make(chan *commandOutput, config.NumWorkers)
	for i := 0; i < config.NumWorkers; i++ {
		go commandExecutor(commandQueue, writeQueue, config.Commands)
	}
	go slackWriter(rtm, writeQueue)
	slackListener(rtm, commandQueue)
}

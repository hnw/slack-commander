package main

import (
	"flag"
	"io"
	"log"
	"os"
	"strings"

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
	Args        []string
	Stdin       io.Reader
	Stdout      io.Writer
	Stderr      io.Writer
	CleanupFunc func()
	Timeout     int
}

type commandOutput struct {
	commandConfig
	origMessage *slack.MessageEvent
	text        string
	isError     bool
}

type slackInput struct {
	Message     *slack.MessageEvent // 起動メッセージ
	MessageText string              // 起動コマンド平文
}

var config topLevelConfig

func newSlackInput(message *slack.MessageEvent, messageText string) *slackInput {
	i := slackInput{}
	i.Message = message
	i.MessageText = messageText
	return &i
}

func onMessageEvent(rtm *slack.RTM, ev *slack.MessageEvent, commandQueue chan *slackInput) {
	if ev.User == "USLACKBOT" && config.AcceptReminder == false {
		return
	}
	if ev.SubType == "bot_message" && config.AcceptBotMessage == false {
		return
	}
	if ev.ThreadTimestamp != "" && config.AcceptThreadMessage == false {
		return
	}
	if ev.User == "USLACKBOT" && strings.HasPrefix(ev.Text, "Reminder: ") {
		text := strings.TrimPrefix(ev.Text, "Reminder: ")
		text = strings.TrimSuffix(text, ".")
		commandQueue <- newSlackInput(ev, text)
	} else if ev.Text != "" {
		commandQueue <- newSlackInput(ev, ev.Text)
	} else if ev.Attachments != nil {
		if ev.Attachments[0].Pretext != "" {
			// attachmentのpretextとtextを文字列連結してtext扱いにする
			text := ev.Attachments[0].Pretext
			if ev.Attachments[0].Text != "" {
				text = text + "\n" + ev.Attachments[0].Text
			}
			commandQueue <- newSlackInput(ev, text)
		} else if ev.Attachments[0].Text != "" {
			commandQueue <- newSlackInput(ev, ev.Attachments[0].Text)
		}
	}
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
	logger := log.New(os.Stderr, "", log.Lshortfile|log.LstdFlags)
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

	for msg := range rtm.IncomingEvents {
		switch ev := msg.Data.(type) {
		case *slack.HelloEvent:
			logger.Println("[DEBUG] Hello event")

		case *slack.ConnectedEvent:
			logger.Println("[DEBUG] Infos:", ev.Info)
			logger.Println("[INFO] Connection counter:", ev.ConnectionCount)

		case *slack.MessageEvent:
			logger.Printf("[DEBUG] Message: %v, text=%s\n", ev, ev.Text)
			onMessageEvent(rtm, ev, commandQueue)

		case *slack.RTMError:
			logger.Printf("[INFO] Error: %s\n", ev.Error())

		case *slack.InvalidAuthEvent:
			logger.Println("[INFO] Invalid credentials")
			return

		default:
			// Ignore other events..
			//fmt.Printf("[DEBUG] Unexpected: %v\n", msg.Data)
		}
	}
}

package main

import (
	"flag"
	"log"
	"os"

	"github.com/BurntSushi/toml"
	"github.com/hashicorp/logutils"
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
	cfg    Config
	logger *log.Logger
)

func main() {
	var (
		quiet   = flag.Bool("q", false, "Quiet mode")
		verbose = flag.Bool("v", false, "Verbose mode")
		debug   = flag.Bool("debug", false, "Debug mode") // slack-go/slackのdebug mode
	)
	flag.Parse()

	// ログレベル""はライブラリ自体のデバッグログ
	// systemdのログに残るのがイヤなので、デフォルトでは表示優先度最低にする
	logLevels := []logutils.LogLevel{"", "DEBUG", "INFO", "WARN", "ERROR"}
	logMinLevel := "WARN"
	if *verbose {
		logMinLevel = "INFO"
	}
	if *debug {
		// debugモードのときは全ログを出す
		logMinLevel = ""
	}
	if *quiet {
		logMinLevel = "ERROR"
	}

	filter := &logutils.LevelFilter{
		Levels:   logLevels,
		MinLevel: logutils.LogLevel(logMinLevel),
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

	api := slack.New(
		cfg.SlackBotToken,
		slack.OptionDebug(*debug),
		slack.OptionLog(logger),
		slack.OptionAppLevelToken(cfg.SlackAppToken),
	)

	smc := socketmode.New(
		api,
		socketmode.OptionDebug(true), // ロギングにsmc.Debugf()を使いたいので常時true
		socketmode.OptionLog(logger),
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

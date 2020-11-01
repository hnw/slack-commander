package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/hashicorp/logutils"
	"github.com/mattn/go-shellwords"
	"github.com/nlopes/slack"
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

type commandMatcher struct {
	Regexps  []*regexp.Regexp
	Commands []*commandConfig
}

type commandInfo struct {
	Message       *slack.MessageEvent // 起動メッセージ
	MessageText   string              // 起動コマンド平文
	Config        *commandConfig      // マッチしたコマンド設定
	Output        string              // 出力（一部のこともある）
	ErrorOccurred bool                // エラー発生したかどうか（この値に応じて色を変える）
}

var (
	mu       sync.Mutex
	lastSent time.Time
	matcher  commandMatcher
	config   topLevelConfig
)

func newCommandInfo(message *slack.MessageEvent, messageText string) *commandInfo {
	info := commandInfo{}
	info.Message = message
	info.MessageText = messageText
	return &info
}

func onMessageEvent(rtm *slack.RTM, ev *slack.MessageEvent, commandQueue chan *commandInfo) {
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
		commandQueue <- newCommandInfo(ev, text)
	} else if ev.Text != "" {
		commandQueue <- newCommandInfo(ev, ev.Text)
	} else if ev.Attachments != nil {
		if ev.Attachments[0].Pretext != "" {
			// attachmentのpretextとtextを文字列連結してtext扱いにする
			text := ev.Attachments[0].Pretext
			if ev.Attachments[0].Text != "" {
				text = text + "\n" + ev.Attachments[0].Text
			}
			commandQueue <- newCommandInfo(ev, text)
		} else if ev.Attachments[0].Text != "" {
			commandQueue <- newCommandInfo(ev, ev.Attachments[0].Text)
		}
	}
}

func initMatcher(cmds []*commandConfig, m *commandMatcher) error {
	regexps := make([]*regexp.Regexp, 0, len(cmds))
	commands := make([]*commandConfig, 0, len(cmds))
	reWildcard := regexp.MustCompile(`(^| )\\\*( |$)`)

	//　commandConfig.Keyword のワイルドカードを正規表現に書き換え
	for _, cmd := range cmds {
		if cmd.Keyword == "" || strings.Count(cmd.Keyword, "*") >= 2 {
			continue
		}
		pattern := regexp.QuoteMeta(cmd.Keyword)
		pattern = reWildcard.ReplaceAllString(pattern, "(?:^| )(.*)(?: |$)")
		pattern = "^" + pattern + "$"
		re := regexp.MustCompile(pattern)
		regexps = append(regexps, re)
		commands = append(commands, cmd)
	}
	m.Regexps = regexps
	m.Commands = commands
	return nil
}

func (m *commandMatcher) matchedCommand(msg string) (*commandConfig, string) {
	for i, re := range m.Regexps {
		matches := re.FindAllStringSubmatch(msg, 1)
		if matches != nil {
			submatched := ""
			if len(matches[0]) >= 2 {
				submatched = matches[0][1]
			}
			return m.Commands[i], submatched
		}
	}
	return nil, ""
}

func (c *commandConfig) buildCommandParams(wildcard string) ([]string, error) {
	p := shellwords.NewParser()
	args, err := p.Parse(c.Command)
	if err != nil {
		return nil, err
	}
	if len(args) == 0 {
		return nil, errors.New("failed to parse `command`")
	}
	//　commandConfig.Command のワイルドカード部分をマッチした文字列に書き換え
	var newArgs []string
	if wildcard != "" {
		for i, v := range args {
			if v == "*" {
				if strings.Index(c.Command, `'*'`) != -1 {
					// '*' なら何もしない
					newArgs = args
					break
				}
				newArgs = append(newArgs, args[:i]...)
				if strings.Index(c.Command, `"*"`) != -1 {
					// "*" なら1引数として展開
					newArgs = append(newArgs, wildcard)
				} else {
					// * なら複数引数として展開
					wildcards, err := p.Parse(wildcard)
					if err != nil {
						return []string{}, err
					}
					newArgs = append(newArgs, wildcards...)
				}
				newArgs = append(newArgs, args[i+1:]...)
				break
			}
		}
	}
	if len(newArgs) == 0 {
		newArgs = args
	}
	return newArgs, nil
}

func execCommand(info *commandInfo, writeQueue chan *commandInfo) {
	msgArr := strings.SplitN(info.MessageText, "\n", 2)
	cmdText := msgArr[0]
	inputText := ""
	if len(msgArr) >= 2 {
		// メッセージが複数行だった場合、1行目をコマンド、2行目以降を標準入力として扱う
		inputText = msgArr[1]
	}
	cmdConfig, wildcard := matcher.matchedCommand(cmdText)
	if cmdConfig == nil {
		return
	}
	args, err := cmdConfig.buildCommandParams(wildcard)
	if err != nil {
		fmt.Printf("%v\n", err)
		return
	}

	cmd := exec.Command(args[0], args[1:]...)

	stdInfo := *info
	stdInfo.Config = cmdConfig
	stdoutWriter := stdInfo.getQueueWriter(writeQueue)
	cmd.Stdin = strings.NewReader(inputText)
	cmd.Stdout = stdoutWriter
	defer stdoutWriter.Flash()

	errInfo := *info
	errInfo.Config = cmdConfig
	errInfo.ErrorOccurred = true
	stderrWriter := errInfo.getQueueWriter(writeQueue)
	cmd.Stderr = stderrWriter
	defer stderrWriter.Flash()

	if err := cmd.Start(); err != nil {
		stderrWriter.Write([]byte(fmt.Sprintf("%v", err)))
		return
	}
	var timer *time.Timer
	if cmdConfig.Timeout > 0 {
		timer = time.AfterFunc(time.Duration(cmdConfig.Timeout)*time.Second, func() {
			timer.Stop()
			cmd.Process.Kill()
		})
	}
	err = cmd.Wait()
	if cmdConfig.Timeout > 0 {
		timer.Stop()
	}
	if err != nil {
		switch err.(type) {
		case *exec.ExitError:
			if exitError, ok := err.(*exec.ExitError); ok {
				if exitError.ExitCode() == -1 {
					errText := fmt.Sprintf("timeout %ds exceeded", cmdConfig.Timeout)
					stderrWriter.Write([]byte(errText))
				}
			}
		default:
			stderrWriter.Write([]byte(fmt.Sprintf("%v", err)))
		}
	}
	return
}

func (info *commandInfo) getText() string {
	text := info.Output
	if info.Config.Monospaced {
		text = fmt.Sprintf("```%s```", text)
	}
	return text
}

func (info *commandInfo) getQueueWriter(writeQueue chan *commandInfo) *slackBuffer {
	b := slackBuffer{}
	b.queue = &writeQueue
	b.info = info
	return &b
}

func (info *commandInfo) getColor() string {
	if info.ErrorOccurred {
		return "danger"
	}
	return "good"
}

func (info *commandInfo) getThreadTimestamp() string {
	if info.Config.PostAsReply {
		return info.Message.Timestamp
	}
	return ""
}

func (info *commandInfo) getReplyBroadcast() bool {
	if info.Config.PostAsReply == false {
		return false
	}
	if info.Config.AlwaysBroadcast {
		return true
	}
	if info.ErrorOccurred {
		return true
	}
	return false
}

func (info *commandInfo) postMessage(rtm *slack.RTM) error {
	params := slack.PostMessageParameters{
		Username:        info.Config.Username,
		IconEmoji:       info.Config.IconEmoji,
		IconURL:         info.Config.IconURL,
		ThreadTimestamp: info.getThreadTimestamp(),
		ReplyBroadcast:  info.getReplyBroadcast(),
	}
	attachment := slack.Attachment{
		Text:  info.getText(),
		Color: info.getColor(),
	}
	msgOptParams := slack.MsgOptionPostMessageParameters(params)
	msgOptAttachment := slack.MsgOptionAttachments(attachment)
	if _, _, err := rtm.PostMessage(info.Message.Channel, msgOptParams, msgOptAttachment); err != nil {
		fmt.Printf("%s\n", err)
		return err
	}
	return nil
}

func slackWriter(rtm *slack.RTM, writeQueue chan *commandInfo) {
	for {
		info, ok := <-writeQueue // closeされると ok が false になる
		if !ok {
			return
		}
		if info.Output != "" {
			info.postMessage(rtm)
		}
		time.Sleep(1 * time.Second)
	}
}
func commandExecutor(commandQueue chan *commandInfo, writeQueue chan *commandInfo) {
	for {
		info, ok := <-commandQueue // closeされると ok が false になる
		if !ok {
			return
		}
		execCommand(info, writeQueue)
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
	if err := initMatcher(config.Commands, &matcher); err != nil {
		logger.Println("[ERROR] ", err)
		return
	}
	optionLogger := slack.OptionLog(logger)
	optionDebug := slack.OptionDebug(*verbose)
	api := slack.New(config.SlackToken, optionLogger, optionDebug)

	rtm := api.NewRTM()
	go rtm.ManageConnection()
	commandQueue := make(chan *commandInfo, config.NumWorkers)
	writeQueue := make(chan *commandInfo, config.NumWorkers)
	for i := 0; i < config.NumWorkers; i++ {
		go commandExecutor(commandQueue, writeQueue)
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

type slackBuffer struct {
	buffer bytes.Buffer
	queue  *(chan *commandInfo)
	info   *commandInfo
	timer  *time.Timer
}

func (b *slackBuffer) Write(data []byte) (n int, err error) {
	//fmt.Printf("len=%d\n", len(data))
	if b.timer != nil {
		b.timer.Stop()
	}
	l := b.buffer.Len() + len(data)
	i := 0
	for l > 2000 {
		writeSize := 2000
		//fmt.Printf("====================\n")
		if b.buffer.Len() > 0 {
			//fmt.Printf("%s", b.buffer.Bytes())
			writeSize -= b.buffer.Len()
			b.buffer.Truncate(0)
		}
		hunk := data[i : i+writeSize]
		b.info.Output = fmt.Sprintf("%s%s", b.buffer.Bytes(), hunk)
		tmp := *(b.info)
		*(b.queue) <- &tmp
		i += writeSize
		l -= 2000
	}
	n, err = b.buffer.Write(data[i:])
	// 最後に出力されてから3秒間何も出力されなければflashする
	b.timer = time.AfterFunc(3*time.Second, func() {
		b.timer.Stop()
		b.Flash()
	})
	n += i //今回のWriteで書き込まれた総バイト数
	return
}

func (b *slackBuffer) Flash() {
	b.info.Output = fmt.Sprintf("%s", b.buffer.Bytes())
	b.buffer.Truncate(0)
	tmp := *(b.info)
	*(b.queue) <- &tmp
}

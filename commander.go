package main

import (
	"bytes"
	"fmt"
	"log"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/BurntSushi/toml"
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
	ThreadTimestamp     string `toml:"thread_ts"`
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
			commandQueue <- newCommandInfo(ev, ev.Attachments[0].Pretext)
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
		return []string{}, err
	}
	//　commandConfig.Command のワイルドカード部分をマッチした文字列に書き換え
	if wildcard != "" {
		for i, v := range args {
			if v == "*" {
				var newArgs []string
				if strings.Index(c.Command, `'*'`) != -1 {
					break
				} else if strings.Index(c.Command, `"*"`) != -1 {
					// "*" なら1引数として展開
					newArgs = append(args[:i], wildcard)
				} else {
					// * なら複数引数として展開
					wildcards, err := p.Parse(wildcard)
					if err != nil {
						return []string{}, err
					}
					newArgs = append(args[:i], wildcards...)
				}
				args = append(newArgs, args[i+1:]...)
				break
			}
		}
	}
	return args, nil
}

func execCommand(info *commandInfo, writeQueue chan *commandInfo) {
	cmdText := strings.TrimSpace(info.MessageText)
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
	cmd.Stdout = stdoutWriter
	defer stdoutWriter.Flash()

	errInfo := *info
	errInfo.Config = cmdConfig
	errInfo.ErrorOccurred = true
	stderrWriter := errInfo.getQueueWriter(writeQueue)
	cmd.Stderr = stderrWriter
	defer stderrWriter.Flash()

	if err := cmd.Start(); err != nil {
		errInfo.Output = fmt.Sprintf("%v", err)
		writeQueue <- &errInfo
		return
	}
	var timer *time.Timer
	timer = time.AfterFunc(time.Duration(cmdConfig.Timeout)*time.Second, func() {
		timer.Stop()
		cmd.Process.Kill()
	})
	err = cmd.Wait()
	timer.Stop()
	if err != nil {
		switch err.(type) {
		case *exec.ExitError:
			if exitError, ok := err.(*exec.ExitError); ok {
				if exitError.ExitCode() == -1 {
					stderrWriter.Write([]byte("タイムアウトしました"))
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
	config = topLevelConfig{NumWorkers: 1}
	if _, err := toml.DecodeFile("config.toml", &config); err != nil {
		fmt.Println(err)
		return
	}
	if err := initMatcher(config.Commands, &matcher); err != nil {
		fmt.Println(err)
		return
	}
	logger := log.New(os.Stdout, "slack-bot: ", log.Lshortfile|log.LstdFlags)
	optionLogger := slack.OptionLog(logger)
	optionDebug := slack.OptionDebug(true)
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
			fmt.Println("Hello event")

		case *slack.ConnectedEvent:
			fmt.Println("Infos:", ev.Info)
			fmt.Println("Connection counter:", ev.ConnectionCount)

		case *slack.MessageEvent:
			fmt.Printf("Message: %v, text=%s\n", ev, ev.Text)
			onMessageEvent(rtm, ev, commandQueue)

		case *slack.RTMError:
			fmt.Printf("Error: %s\n", ev.Error())

		case *slack.InvalidAuthEvent:
			fmt.Printf("Invalid credentials")
			return

		default:
			// Ignore other events..
			// fmt.Printf("Unexpected: %v\n", msg.Data)
		}
	}
}

type slackBuffer struct {
	buffer bytes.Buffer
	queue  *(chan *commandInfo)
	info   *commandInfo
}

func (b *slackBuffer) Write(data []byte) (n int, err error) {
	//fmt.Printf("len=%d\n", len(data))
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
	n += i
	return
}
func (b *slackBuffer) Flash() {
	b.info.Output = fmt.Sprintf("%s", b.buffer.Bytes())
	*(b.queue) <- b.info
}

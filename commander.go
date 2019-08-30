package main

import (
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

type tomlConfig struct {
	SlackToken string `toml:"slack_token"`
	Commands   []*commandConfig
}

type commandConfig struct {
	Keyword    string
	Command    string
	Aliases    []string
	Monospaced bool
}
type cmdMatcher struct {
	Regexps  []*regexp.Regexp
	Commands []*commandConfig
}

var (
	mu       sync.Mutex
	lastSent time.Time
	matcher  cmdMatcher
)

func onMessageEvent(rtm *slack.RTM, ev *slack.MessageEvent) {
	ret := ""
	if ev.User == "USLACKBOT" && strings.HasPrefix(ev.Text, "Reminder: ") {
		text := strings.TrimPrefix(ev.Text, "Reminder: ")
		text = strings.TrimSuffix(text, ".")
		ret = execCommand(text)
	} else if ev.Text != "" {
		ret = execCommand(ev.Text)
	} else if ev.Attachments != nil {
		if ev.Attachments[0].Pretext != "" {
			ret = execCommand(ev.Attachments[0].Pretext)
		} else if ev.Attachments[0].Text != "" {
			ret = execCommand(ev.Attachments[0].Text)
		}
	}
	if ret != "" {
		sendMessage(rtm, ev.Channel, ret)
	}
}

func initMatcher(cmds []*commandConfig, m *cmdMatcher) error {
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

func (m *cmdMatcher) matchedCommand(msg string) (*commandConfig, string) {
	fmt.Printf("m=%v", m)
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

func execCommand(msg string) string {
	msg = strings.TrimSpace(msg)
	cmdConfig, wildcard := matcher.matchedCommand(msg)
	if cmdConfig == nil {
		return ""
	}
	args, err := cmdConfig.buildCommandParams(wildcard)
	if err != nil {
		return fmt.Sprintf("%v\n", err)
	}
	out, err := exec.Command(args[0], args[1:]...).Output()
	if err != nil {
		switch err.(type) {
		case *exec.ExitError:
			out = err.(*exec.ExitError).Stderr
		default:
			out = []byte(fmt.Sprintf("%v", err))
		}
	}
	if cmdConfig.Monospaced {
		codeBlock := []byte("```")
		out = append(codeBlock, out...)
		out = append(out, codeBlock...)
	}
	return string(out)
}

func sendMessage(rtm *slack.RTM, channel, text string) {
	mu.Lock()
	defer mu.Unlock()

	now := time.Now()
	if now.Before(lastSent.Add(time.Second)) {
		return
	}

	msg := rtm.NewOutgoingMessage(text, channel)
	rtm.SendMessage(msg)
	lastSent = now
}

func main() {
	var config tomlConfig
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

	for msg := range rtm.IncomingEvents {
		switch ev := msg.Data.(type) {
		case *slack.HelloEvent:
			fmt.Println("Hello event")

		case *slack.ConnectedEvent:
			fmt.Println("Infos:", ev.Info)
			fmt.Println("Connection counter:", ev.ConnectionCount)

		case *slack.MessageEvent:
			fmt.Printf("Message: %v, text=%s\n", ev, ev.Text)
			onMessageEvent(rtm, ev)

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

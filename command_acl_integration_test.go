package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/hnw/slack-commander/cmd"
	"github.com/hnw/slack-commander/pubsub"
	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
	"github.com/slack-go/slack/socketmode"
)

type aclIntegrationRunner struct {
	calls []string
}

func TestThreadStdinUsesRootCommandACL(t *testing.T) {
	var cfg Config
	if err := decodeConfigString(`
slack_bot_token = "xoxb-test"
slack_app_token = "xapp-test"
num_workers = 1
allowed_user_ids = ["U-root", "U-stdin"]
allowed_channel_ids = ["C"]
output_flush_interval = "0s"

[[commands]]
keyword = "agent"
command = '''/bin/sh -c 'printf "ready\n"; IFS= read -r line; printf "%s\n" "$line"' '''
interaction = "stdin"
allowed_user_ids = ["U-root"]
timeout = 5
`, &cfg); err != nil {
		t.Fatal(err)
	}
	if err := resolveConfig(&cfg); err != nil {
		t.Fatal(err)
	}
	stdinStore := &cmd.StdinStore{}
	commands := buildCommandSet(cfg.commandConfigs, newRunnerFactory(stdinStore))
	requests := make(chan *cmd.CommandInput, 10)
	outputs := make(chan *cmd.CommandOutput, 30)
	conversationLocks := &cmd.ConversationLocks{}
	router := cmd.NewConversationRouterWithRootInputResolver(commands, nil, func(input *cmd.CommandInput) bool {
		requests <- input
		return true
	}, 10)
	ctx, cancel := context.WithCancel(context.Background())
	var workers sync.WaitGroup
	startWorkers(ctx, 1, requests, stdinStore, conversationLocks, cmd.NewExecutor(commands, outputs), &workers)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"ok":true,"user_id":"U-self","bot_id":"B-self"}`)
	}))
	defer server.Close()
	smc := socketmode.New(slack.New("test", slack.OptionAPIURL(server.URL+"/")))
	done := make(chan struct{})
	go func() {
		if err := pubsub.SlackListener(ctx, smc, cfg.PubSubConfig, router); err != nil {
			t.Errorf("SlackListener() error = %v", err)
		}
		close(done)
	}()
	t.Cleanup(func() { cancel(); <-done; workers.Wait() })
	send := func(user, thread, text string) {
		timestamp := "1"
		if thread != "" {
			timestamp = "2"
		}
		smc.Events <- socketmode.Event{Type: socketmode.EventTypeEventsAPI, Data: slackevents.EventsAPIEvent{
			Type: slackevents.CallbackEvent, InnerEvent: slackevents.EventsAPIInnerEvent{Data: &slackevents.MessageEvent{
				User: user, Channel: "C", TimeStamp: timestamp, ThreadTimeStamp: thread, Text: text,
			}},
		}}
	}
	send("U-root", "", "agent")
	awaitThreadStdinReady(t, outputs)
	send("U-stdin", "1", "denied")
	send("U-root", "1", "accepted")
	awaitThreadStdinOutput(t, outputs, "accepted\n")
}

func (r *aclIntegrationRunner) CommandContext(_ context.Context, name string, _ ...string) cmd.Cmd {
	r.calls = append(r.calls, name)
	return &aclIntegrationCmd{}
}

type aclIntegrationCmd struct{}

func (*aclIntegrationCmd) SetStdin(io.Reader)  {}
func (*aclIntegrationCmd) SetStdout(io.Writer) {}
func (*aclIntegrationCmd) SetStderr(io.Writer) {}
func (*aclIntegrationCmd) Run(int) int         { return 0 }

func TestCommandACLThroughSlackListenerAndExecutor(t *testing.T) {
	var cfg Config
	err := decodeConfigString(`
slack_bot_token = "xoxb-test"
slack_app_token = "xapp-test"
num_workers = 1
allowed_user_ids = ["U-top", "U-admin", "U-guest", "B-other"]
allowed_channel_ids = ["C-main", "C-ops"]

[[commands]]
keyword = "run"
command = "status"
allowed_user_ids = ["U-top", "B-other"]
allowed_channel_ids = ["C-main"]

[[commands]]
keyword = "run"
command = "deploy"
interaction = "command"
allowed_user_ids = ["U-admin", "U-guest"]
allowed_channel_ids = ["C-ops"]

[[commands.replies]]
keyword = "retry"
command = "guest-retry"
allowed_user_ids = ["U-guest"]
allowed_channel_ids = ["C-ops"]

[[commands.replies]]
keyword = "retry"
command = "admin-retry"

[[commands]]
keyword = "backup"
command = "backup"
accept_reminder = true
`, &cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := resolveConfig(&cfg); err != nil {
		t.Fatal(err)
	}
	runner := &aclIntegrationRunner{}
	commands := buildCommandSet(cfg.commandConfigs, func(cmd.RunnerConfig) cmd.CommandRunner { return runner })
	queued := make(chan *cmd.CommandInput, 10)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/auth.test" {
			_, _ = io.WriteString(w, `{"ok":true,"user_id":"U-self","bot_id":"B-self"}`)
			return
		}
		if r.URL.Path != "/conversations.replies" {
			t.Errorf("unexpected Slack API path: %s", r.URL.Path)
		}
		_, _ = io.WriteString(w, `{"ok":true,"messages":[{"ts":"1","user":"U-admin","text":"run"}]}`)
	}))
	defer server.Close()
	smc := socketmode.New(slack.New("test", slack.OptionAPIURL(server.URL+"/")))
	router := cmd.NewConversationRouterWithRootInputResolver(commands, pubsub.SlackRootInputResolver(smc, cfg.PubSubConfig), func(input *cmd.CommandInput) bool {
		queued <- input
		return true
	}, 0)
	executor := cmd.NewExecutor(commands, make(chan *cmd.CommandOutput, 30))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		if err := pubsub.SlackListener(ctx, smc, cfg.PubSubConfig, router); err != nil {
			t.Errorf("SlackListener() error = %v", err)
		}
		close(done)
	}()
	t.Cleanup(func() { cancel(); <-done })

	tests := []struct {
		name    string
		event   interface{}
		want    []string
		noQueue bool
	}{
		{"root command applies narrower ACL", &slackevents.MessageEvent{User: "U-admin", Channel: "C-ops", TimeStamp: "1", Text: "run"}, []string{"deploy"}, false},
		{"reply uses narrowed ACL", &slackevents.MessageEvent{User: "U-guest", Channel: "C-ops", TimeStamp: "2", ThreadTimeStamp: "1", Text: "retry"}, []string{"guest-retry"}, false},
		{"reply refuses user outside both sibling ACLs", &slackevents.MessageEvent{User: "U-other", Channel: "C-ops", TimeStamp: "3", ThreadTimeStamp: "1", Text: "retry"}, nil, true},
		{"sibling reply inherits parent ACL", &slackevents.MessageEvent{User: "U-admin", Channel: "C-ops", TimeStamp: "4", ThreadTimeStamp: "1", Text: "retry"}, []string{"admin-retry"}, false},
		{"same keyword selects top-level inherited command", &slackevents.MessageEvent{User: "U-top", Channel: "C-main", TimeStamp: "5", Text: "run"}, []string{"status"}, false},
		{"reminder bypasses user allowlist", &slackevents.MessageEvent{User: "USLACKBOT", Channel: "C-main", TimeStamp: "6", Text: "Reminder: backup."}, []string{"backup"}, false},
		{"reminder still checks channel", &slackevents.MessageEvent{User: "USLACKBOT", Channel: "C-other", TimeStamp: "7", Text: "Reminder: backup."}, nil, true},
		{"app mention uses command override", &slackevents.AppMentionEvent{User: "U-admin", Channel: "C-ops", TimeStamp: "8", Text: "<@BOT> run"}, []string{"deploy"}, false},
		{"bot sender ID is allowlisted as sender", &slackevents.MessageEvent{BotID: "B-other", SubType: "bot_message", Channel: "C-main", TimeStamp: "9", Text: "run"}, []string{"status"}, false},
		{"bot ID takes precedence over allowlisted user ID", &slackevents.MessageEvent{User: "U-top", BotID: "B-denied", SubType: "bot_message", Channel: "C-main", TimeStamp: "10", Text: "run"}, nil, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			smc.Events <- socketmode.Event{Type: socketmode.EventTypeEventsAPI, Data: slackevents.EventsAPIEvent{
				Type: slackevents.CallbackEvent, InnerEvent: slackevents.EventsAPIInnerEvent{Data: tt.event},
			}}
			if tt.noQueue {
				select {
				case input := <-queued:
					t.Fatalf("input without ACL candidates was queued: %+v", input)
				case <-time.After(100 * time.Millisecond):
				}
				return
			}
			select {
			case input := <-queued:
				runner.calls = nil
				executor.Execute(context.Background(), input, nil)
				if !slices.Equal(runner.calls, tt.want) {
					t.Fatalf("executed = %v, want %v; candidates = %v", runner.calls, tt.want, input.AllowedCommandIndexes)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("Listener did not route the input")
			}
		})
	}
}

func TestCommandACLCandidatesInChains(t *testing.T) {
	tests := []struct {
		name    string
		text    string
		allowed []int
		want    []string
	}{
		{"all allowed", "status ; deploy", []int{0, 1}, []string{"status", "deploy"}},
		{"denied specific falls through to allowed wildcard", "status ; deploy", []int{0, 2}, []string{"status", "generic"}},
		{"denied first command can fall through to allowed wildcard", "deploy ; status", []int{0, 2}, []string{"generic", "status"}},
		{"allow in chain still enforced", "status ; interact", []int{0, 3}, nil},
		{"empty candidates", "status ; deploy", []int{}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runner := &aclIntegrationRunner{}
			configs := []cmd.CommandConfig{
				{Index: 0, MatcherConfig: cmd.MatcherConfig{RawMatcherConfig: cmd.RawMatcherConfig{Keyword: "status"}}, ParserConfig: cmd.ParserConfig{AllowInChain: true}, RunnerConfig: cmd.RunnerConfig{RawRunnerConfig: cmd.RawRunnerConfig{Command: "status"}}},
				{Index: 1, MatcherConfig: cmd.MatcherConfig{RawMatcherConfig: cmd.RawMatcherConfig{Keyword: "deploy"}}, ParserConfig: cmd.ParserConfig{AllowInChain: true}, RunnerConfig: cmd.RunnerConfig{RawRunnerConfig: cmd.RawRunnerConfig{Command: "deploy"}}},
				{Index: 2, MatcherConfig: cmd.MatcherConfig{RawMatcherConfig: cmd.RawMatcherConfig{Keyword: "*"}}, ParserConfig: cmd.ParserConfig{AllowInChain: true}, RunnerConfig: cmd.RunnerConfig{RawRunnerConfig: cmd.RawRunnerConfig{Command: "generic *"}}},
				{Index: 3, MatcherConfig: cmd.MatcherConfig{RawMatcherConfig: cmd.RawMatcherConfig{Keyword: "interact"}}, RunnerConfig: cmd.RunnerConfig{RawRunnerConfig: cmd.RawRunnerConfig{Command: "interact"}}},
			}
			commands := make([]*cmd.Command, 0, len(configs))
			for _, config := range configs {
				commands = append(commands, cmd.NewCommand(config, runner, nil))
			}
			executor := cmd.NewExecutor(cmd.NewCommandSet(commands), make(chan *cmd.CommandOutput, 20))
			executor.Execute(context.Background(), &cmd.CommandInput{Text: tt.text, AllowedCommandIndexes: tt.allowed}, nil)
			if !slices.Equal(runner.calls, tt.want) {
				t.Fatalf("executed = %v, want %v", runner.calls, tt.want)
			}
		})
	}
}

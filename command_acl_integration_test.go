package main

import (
	"bufio"
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
timeout = "5s"
`, &cfg); err != nil {
		t.Fatal(err)
	}
	if err := resolveConfig(&cfg); err != nil {
		t.Fatal(err)
	}
	stdinStore := &cmd.StdinStore{}
	commands := buildCommandSet(cfg.commandConfigs, newRunnerFactory())
	requests := make(chan *cmd.CommandInput, 10)
	outputs := make(chan *cmd.CommandOutput, 30)
	conversationLocks := &cmd.ConversationLocks{}
	router := cmd.NewConversationRouter(stdinStore, commands, nil, newMainTestDispatcher(context.Background(), outputs, stdinStore, conversationLocks, func(input *cmd.CommandInput) bool {
		requests <- input
		return true
	}), 10)
	ctx, cancel := context.WithCancel(context.Background())
	var workers sync.WaitGroup
	startWorkers(ctx, 1, requests, stdinStore, conversationLocks, cmd.NewExecutor(outputs), &workers)
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

func TestThreadStdinChainReplyACLTracksActiveCommand(t *testing.T) {
	var cfg Config
	if err := decodeConfigString(`
slack_bot_token = "xoxb-test"
slack_app_token = "xapp-test"
num_workers = 1
allowed_user_ids = ["U-root", "U-a", "U-b"]
allowed_channel_ids = ["C"]
output_flush_interval = "0s"

[[commands]]
keyword = "stdin-a"
command = "stdin-a"
interaction = "stdin"
allowed_user_ids = ["U-root", "U-a"]

[[commands]]
keyword = "stdin-b"
command = "stdin-b"
interaction = "stdin"
allowed_user_ids = ["U-root", "U-b"]
`, &cfg); err != nil {
		t.Fatal(err)
	}
	if err := resolveConfig(&cfg); err != nil {
		t.Fatal(err)
	}
	runner := &aclSwitchRunner{started: make(chan string, 2), delivered: make(chan string, 4), release: map[string]chan struct{}{
		"stdin-a": make(chan struct{}, 1),
		"stdin-b": make(chan struct{}, 1),
	}}
	stdinStore := &cmd.StdinStore{}
	commands := buildCommandSet(cfg.commandConfigs, func(config cmd.RunnerConfig) cmd.CommandRunner {
		if config.Runner == cmd.RunnerStdinReply {
			return cmd.NewStdinReplyRunner()
		}
		return runner.forCommand(config.Command)
	})
	requests := make(chan *cmd.CommandInput, 10)
	outputs := make(chan *cmd.CommandOutput, 30)
	conversationLocks := &cmd.ConversationLocks{}
	ctx, cancel := context.WithCancel(context.Background())
	dispatcher := cmd.NewCommandDispatcher(ctx, cmd.NewExecutor(outputs), outputs, stdinStore, conversationLocks, func(input *cmd.CommandInput) bool {
		requests <- input
		return true
	})
	router := cmd.NewConversationRouter(stdinStore, commands, nil, dispatcher, 0)
	var workers sync.WaitGroup
	startWorkers(ctx, 1, requests, stdinStore, conversationLocks, cmd.NewExecutor(outputs), &workers)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"ok":true,"user_id":"U-self","bot_id":"B-self"}`)
	}))
	defer server.Close()
	smc := socketmode.New(slack.New("test", slack.OptionAPIURL(server.URL+"/")))
	listenerDone := make(chan struct{})
	go func() {
		if err := pubsub.SlackListener(ctx, smc, cfg.PubSubConfig, router); err != nil {
			t.Errorf("SlackListener() error = %v", err)
		}
		close(listenerDone)
	}()
	t.Cleanup(func() {
		for _, name := range []string{"stdin-a", "stdin-b"} {
			select {
			case runner.release[name] <- struct{}{}:
			default:
			}
		}
		cancel()
		<-listenerDone
		dispatcher.Close()
		dispatcher.Wait()
		workers.Wait()
	})
	send := func(user, timestamp, text string) {
		t.Helper()
		threadTimestamp := "1"
		if timestamp == "1" {
			threadTimestamp = ""
		}
		smc.Events <- socketmode.Event{Type: socketmode.EventTypeEventsAPI, Data: slackevents.EventsAPIEvent{
			Type: slackevents.CallbackEvent, InnerEvent: slackevents.EventsAPIInnerEvent{Data: &slackevents.MessageEvent{
				User: user, Channel: "C", TimeStamp: timestamp, ThreadTimeStamp: threadTimestamp, Text: text,
			}},
		}}
	}
	waitStarted := func(want string) {
		t.Helper()
		select {
		case got := <-runner.started:
			if got != want {
				t.Fatalf("active command = %q, want %q", got, want)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("command %q did not start", want)
		}
	}
	waitDelivered := func(want string) {
		t.Helper()
		select {
		case got := <-runner.delivered:
			if got != want+"\n" {
				t.Fatalf("stdin line = %q, want %q", got, want+"\n")
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("reply %q was not delivered", want)
		}
	}
	send("U-root", "1", "stdin-a ; stdin-b")
	waitStarted("stdin-a")
	send("U-b", "2", "denied-by-a")
	send("U-root", "3", "accepted-root-by-a")
	waitDelivered("accepted-root-by-a")
	send("U-a", "4", "accepted-by-a")
	waitDelivered("accepted-by-a")
	runner.release["stdin-a"] <- struct{}{}
	waitStarted("stdin-b")
	send("U-a", "5", "denied-by-b")
	send("U-root", "6", "accepted-root-by-b")
	waitDelivered("accepted-root-by-b")
	send("U-b", "7", "accepted-by-b")
	waitDelivered("accepted-by-b")
	runner.release["stdin-b"] <- struct{}{}
}

type aclSwitchRunner struct {
	started   chan string
	delivered chan string
	release   map[string]chan struct{}
}

func (r *aclSwitchRunner) forCommand(name string) cmd.CommandRunner {
	return aclSwitchCommandRunner{runner: r, name: name}
}

type aclSwitchCommandRunner struct {
	runner *aclSwitchRunner
	name   string
}

func (r aclSwitchCommandRunner) CommandContext(context.Context, string, ...string) cmd.Cmd {
	return aclSwitchCommand(r)
}

type aclSwitchCommand struct {
	runner *aclSwitchRunner
	name   string
}

func (aclSwitchCommand) SetStdin(io.Reader)  {}
func (aclSwitchCommand) SetStdout(io.Writer) {}
func (aclSwitchCommand) SetStderr(io.Writer) {}
func (aclSwitchCommand) Run() int            { return 0 }
func (c aclSwitchCommand) RunWithStdin(start func(io.WriteCloser)) int {
	reader, writer := io.Pipe()
	defer func() { _ = reader.Close(); _ = writer.Close() }()
	start(writer)
	c.runner.started <- c.name
	go func() {
		input := bufio.NewReader(reader)
		for {
			line, err := input.ReadString('\n')
			if err != nil {
				return
			}
			c.runner.delivered <- line
		}
	}()
	<-c.runner.release[c.name]
	return 0
}

func (r *aclIntegrationRunner) CommandContext(_ context.Context, name string, _ ...string) cmd.Cmd {
	r.calls = append(r.calls, name)
	return &aclIntegrationCmd{}
}

type aclIntegrationCmd struct{}

func (*aclIntegrationCmd) SetStdin(io.Reader)  {}
func (*aclIntegrationCmd) SetStdout(io.Writer) {}
func (*aclIntegrationCmd) SetStderr(io.Writer) {}
func (*aclIntegrationCmd) Run() int            { return 0 }

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
	outputs := make(chan *cmd.CommandOutput, 30)
	executor := cmd.NewExecutor(outputs)
	stdinStore := &cmd.StdinStore{}
	dispatcher := cmd.NewCommandDispatcher(context.Background(), executor, outputs, stdinStore, &cmd.ConversationLocks{}, func(input *cmd.CommandInput) bool {
		queued <- input
		return true
	})
	router := cmd.NewConversationRouter(stdinStore, commands, pubsub.SlackRootInputResolver(smc, cfg.PubSubConfig), dispatcher, 0)
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
			commandSet := cmd.NewCommandSet(commands)
			executor := cmd.NewExecutor(make(chan *cmd.CommandOutput, 20))
			var queued *cmd.CommandInput
			dispatcher := cmd.NewCommandDispatcher(context.Background(), executor, make(chan *cmd.CommandOutput, 20), &cmd.StdinStore{}, &cmd.ConversationLocks{}, func(input *cmd.CommandInput) bool {
				queued = input
				return true
			})
			defer func() { dispatcher.Close(); dispatcher.Wait() }()
			input := &cmd.CommandInput{Text: tt.text, AllowedCommandIndexes: tt.allowed}
			input.ResolvedInput = commandSet.ResolveInput(tt.text, tt.allowed)
			result := dispatcher.Dispatch(input)
			if len(tt.want) == 0 {
				if result != cmd.DispatchIgnored || queued != nil {
					t.Fatalf("Dispatch() = %v, queued=%p; want ignored", result, queued)
				}
			} else if result != cmd.DispatchAccepted || queued != input {
				t.Fatalf("Dispatch() = %v, queued=%p; want accepted input", result, queued)
			}
			if queued != nil {
				executor.Execute(context.Background(), queued, nil)
			}
			if !slices.Equal(runner.calls, tt.want) {
				t.Fatalf("executed = %v, want %v", runner.calls, tt.want)
			}
		})
	}
}

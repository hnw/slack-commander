package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/hnw/slack-commander/cmd"
	"github.com/hnw/slack-commander/pubsub"
)

func TestDecodeConfigRejectsInvalidTOML(t *testing.T) {
	tests := []struct {
		name string
		text string
		want []string
	}{
		{name: "unknown top-level field", text: "unknown = true", want: []string{"unknown", "field", "1|"}},
		{name: "unknown command field", text: "[[commands]]\nunknown = true", want: []string{"unknown", "field", "2|"}},
		{name: "unknown reply field", text: "[[commands]]\n[[commands.replies]]\nunknown = true", want: []string{"unknown", "field", "3|"}},
		{name: "command index is internal", text: "[[commands]]\ncommand_index = 42", want: []string{"command_index", "field"}},
		{name: "top-level reminder flag is command-only", text: "accept_reminder = true", want: []string{"accept_reminder", "field"}},
		{name: "bot message flag is removed", text: "accept_bot_message = true", want: []string{"accept_bot_message", "field"}},
		{name: "bot message flag is removed from commands", text: "[[commands]]\naccept_bot_message = true", want: []string{"accept_bot_message", "field"}},
		{name: "bot message flag is removed from replies", text: "[[commands]]\n[[commands.replies]]\naccept_bot_message = true", want: []string{"accept_bot_message", "field"}},
		{name: "type mismatch", text: "num_workers = 'one'", want: []string{"num_workers", "1|"}},
		{name: "syntax error", text: "num_workers =", want: []string{"num_workers", "1|"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var cfg Config
			err := decodeConfig(strings.NewReader(tt.text), &cfg)
			if err == nil {
				t.Fatal("decodeConfig() accepted invalid configuration")
			}
			if strings.Contains(err.Error(), "invalid configuration:") {
				t.Fatalf("decode error has validation heading: %v", err)
			}
			for _, want := range tt.want {
				if !strings.Contains(err.Error(), want) {
					t.Fatalf("decodeConfig() error = %q, want it to contain %q", err, want)
				}
			}
		})
	}
}

func TestDecodeConfigRejectsRuntimeOnlyCommandFields(t *testing.T) {
	for _, tc := range []struct {
		field string
		value string
	}{
		{field: "command_index", value: "0"},
		{field: "is_reply", value: "true"},
		{field: "synthetic_stdin_reply", value: "true"},
	} {
		t.Run(tc.field, func(t *testing.T) {
			var cfg Config
			err := decodeConfigString("[[commands]]\nkeyword = \"run\"\n"+tc.field+" = "+tc.value+"\n", &cfg)
			if err == nil || !strings.Contains(err.Error(), tc.field) {
				t.Fatalf("decodeConfig error = %v, want unknown field %q", err, tc.field)
			}
		})
	}
}

func TestResolveCommandConfigKeepsOnlyInteractionReplies(t *testing.T) {
	for _, tc := range []struct {
		interaction     string
		wantReplies     int
		wantDirectReply bool
	}{
		{interaction: cmd.InteractionOneshot},
		{interaction: cmd.InteractionStdin, wantReplies: 1, wantDirectReply: true},
		{interaction: cmd.InteractionCommand, wantReplies: 1},
	} {
		t.Run(tc.interaction, func(t *testing.T) {
			raw := &RawCommandConfig{RawMatcherConfig: cmd.RawMatcherConfig{Keyword: "run"}, RawRunnerConfig: cmd.RawRunnerConfig{Command: "run"}, Interaction: tc.interaction, Replies: []*RawCommandConfig{{RawMatcherConfig: cmd.RawMatcherConfig{Keyword: "retry"}, RawRunnerConfig: cmd.RawRunnerConfig{Command: "retry"}}}}
			resolved, err := resolveCommandConfig(raw, pubsub.DefaultOutputFlushInterval, 1)
			if err != nil {
				t.Fatal(err)
			}
			if len(resolved.Replies) != tc.wantReplies {
				t.Fatalf("resolved replies = %d, want %d", len(resolved.Replies), tc.wantReplies)
			}
			if tc.wantDirectReply && (resolved.Replies[0].Runner != cmd.RunnerStdinReply || resolved.Replies[0].Command != "stdin-reply" || resolved.Replies[0].Dispatch != cmd.DispatchRunner || resolved.Replies[0].Keyword != "*" || resolved.Replies[0].InputBodyMode != cmd.InputBodyRawStdin || resolved.InputBodyMode != cmd.InputBodyStdin) {
				t.Fatalf("stdin reply = %+v, want stdin runner direct reply", resolved.Replies[0])
			}
			if !tc.wantDirectReply && tc.wantReplies == 1 && (resolved.Replies[0].Keyword != "retry" || resolved.Replies[0].Dispatch != cmd.DispatchQueue) {
				t.Fatalf("configured reply = %+v, want queued retry", resolved.Replies[0])
			}
		})
	}
}

func TestResolveCommandConfigAssignsDispatchMode(t *testing.T) {
	for _, tc := range []struct {
		runner string
		want   cmd.DispatchMode
	}{
		{runner: cmd.RunnerExec, want: cmd.DispatchQueue},
		{runner: cmd.RunnerCompose, want: cmd.DispatchQueue},
		{runner: cmd.RunnerHTTP, want: cmd.DispatchExecutor},
	} {
		t.Run(tc.runner, func(t *testing.T) {
			raw := &RawCommandConfig{
				RawMatcherConfig: cmd.RawMatcherConfig{Keyword: "run"},
				RawRunnerConfig:  cmd.RawRunnerConfig{Runner: tc.runner, Command: "run", Method: "GET", URL: "https://example.com"},
			}
			resolved, err := resolveCommandConfig(raw, pubsub.DefaultOutputFlushInterval, 1)
			if err != nil {
				t.Fatal(err)
			}
			if resolved.Dispatch != tc.want {
				t.Fatalf("Dispatch = %v, want %v", resolved.Dispatch, tc.want)
			}
		})
	}
}

func TestReplyDispatchModeFollowsResolvedRunner(t *testing.T) {
	raw := &RawCommandConfig{
		RawMatcherConfig: cmd.RawMatcherConfig{Keyword: "lookup"},
		RawRunnerConfig:  cmd.RawRunnerConfig{Runner: cmd.RunnerHTTP, Method: "GET", URL: "https://example.com"},
		Interaction:      cmd.InteractionCommand,
		Replies: []*RawCommandConfig{
			{RawMatcherConfig: cmd.RawMatcherConfig{Keyword: "inherited"}, RawRunnerConfig: cmd.RawRunnerConfig{Method: "GET", URL: "https://example.com"}},
			{RawMatcherConfig: cmd.RawMatcherConfig{Keyword: "overridden"}, RawRunnerConfig: cmd.RawRunnerConfig{Runner: cmd.RunnerExec, Command: "run"}},
		},
	}
	resolved, err := resolveCommandConfig(raw, pubsub.DefaultOutputFlushInterval, 1)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Replies[0].Runner != cmd.RunnerHTTP || resolved.Replies[0].Dispatch != cmd.DispatchExecutor {
		t.Fatalf("inherited reply = %+v, want HTTP Executor policy", resolved.Replies[0])
	}
	if resolved.Replies[1].Runner != cmd.RunnerExec || resolved.Replies[1].Dispatch != cmd.DispatchQueue {
		t.Fatalf("overridden reply = %+v, want exec Queue policy", resolved.Replies[1])
	}
}

func TestResolveConfigFlattensCommandListenerPolicies(t *testing.T) {
	text := `slack_bot_token = "xoxb-test"
slack_app_token = "xapp-test"
num_workers = 1
allowed_user_ids = ["U-top", "U-admin"]
allowed_channel_ids = ["C-top"]

[[commands]]
keyword = "status"
command = "status"
interaction = "command"
accept_reminder = true

[[commands.replies]]
keyword = "retry"
command = "retry"
allowed_user_ids = []

[[commands.replies]]
keyword = "cancel"
command = "cancel"
allowed_channel_ids = []
accept_reminder = true

[[commands]]
keyword = "deploy"
command = "deploy"
allowed_user_ids = ["U-admin"]
allowed_channel_ids = []
accept_reminder = false
`
	var cfg Config
	if err := decodeConfigString(text, &cfg); err != nil {
		t.Fatal(err)
	}
	if err := resolveConfig(&cfg); err != nil {
		t.Fatal(err)
	}

	want := []pubsub.ListenerConfig{
		{CommandIndex: 0, RawListenerConfig: pubsub.RawListenerConfig{AllowedUserIDs: []string{"U-top", "U-admin"}, AllowedChannelIDs: []string{"C-top"}, AcceptReminder: true}},
		{CommandIndex: 1, IsReply: true, RawListenerConfig: pubsub.RawListenerConfig{AllowedUserIDs: []string{"U-top", "U-admin"}, AllowedChannelIDs: []string{"C-top"}}},
		{CommandIndex: 2, IsReply: true, RawListenerConfig: pubsub.RawListenerConfig{AllowedUserIDs: []string{"U-top", "U-admin"}, AllowedChannelIDs: []string{"C-top"}, AcceptReminder: true}},
		{CommandIndex: 3, RawListenerConfig: pubsub.RawListenerConfig{AllowedUserIDs: []string{"U-admin"}, AllowedChannelIDs: []string{"C-top"}}},
	}
	if !reflect.DeepEqual(cfg.ListenerConfigs, want) {
		t.Fatalf("ListenerConfigs = %#v, want %#v", cfg.ListenerConfigs, want)
	}
	if cfg.commandConfigs[0].Index != 0 || cfg.commandConfigs[0].Replies[0].Index != 1 || cfg.commandConfigs[0].Replies[1].Index != 2 || cfg.commandConfigs[1].Index != 3 {
		t.Fatalf("command indexes = root %d, replies %d/%d, second root %d", cfg.commandConfigs[0].Index, cfg.commandConfigs[0].Replies[0].Index, cfg.commandConfigs[0].Replies[1].Index, cfg.commandConfigs[1].Index)
	}
}

func resolveStdinReplyTestConfig(t *testing.T) (*Config, *resolvedCommandConfig, *resolvedCommandConfig) {
	t.Helper()
	reply := &RawCommandConfig{RawMatcherConfig: cmd.RawMatcherConfig{Keyword: "retry"}, RawRunnerConfig: cmd.RawRunnerConfig{Command: "retry"}}
	root := &RawCommandConfig{
		RawMatcherConfig:  cmd.RawMatcherConfig{Keyword: "agent"},
		RawRunnerConfig:   cmd.RawRunnerConfig{Command: "agent"},
		Interaction:       cmd.InteractionStdin,
		RawListenerConfig: pubsub.RawListenerConfig{AllowedUserIDs: []string{"U123"}, AllowedChannelIDs: []string{"C123"}, AcceptReminder: true},
		Replies:           []*RawCommandConfig{reply},
	}
	cfg := validTestConfig(root)
	cfg.AllowedChannelIDs = []string{"C123"}
	if err := resolveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	resolved := cfg.commandConfigs[0]
	return cfg, resolved, resolved.Replies[0]
}

func TestResolveConfigBuildsRawStdinReplyWithResolvedRootACL(t *testing.T) {
	cfg, resolved, rawReply := resolveStdinReplyTestConfig(t)
	if len(resolved.Replies) != 1 {
		t.Fatalf("resolved replies = %d, want only raw stdin reply", len(resolved.Replies))
	}
	if rawReply.Index == resolved.Index {
		t.Fatalf("stdin reply = %+v", rawReply)
	}
	if rawReply.ParserConfig != (cmd.ParserConfig{AllowInChain: true, InputBodyMode: cmd.InputBodyRawStdin}) || rawReply.ExecutorConfig != resolved.ExecutorConfig {
		t.Fatalf("stdin reply parser/executor config = %+v/%+v, root executor = %+v", rawReply.ParserConfig, rawReply.ExecutorConfig, resolved.ExecutorConfig)
	}
	if len(cfg.ListenerConfigs) != 2 {
		t.Fatalf("listener configs = %+v", cfg.ListenerConfigs)
	}
	syntheticACL := cfg.ListenerConfigs[1]
	if !syntheticACL.IsReply || syntheticACL.CommandIndex != rawReply.Index || !syntheticACL.AcceptReminder || !reflect.DeepEqual(syntheticACL.AllowedUserIDs, cfg.ListenerConfigs[0].AllowedUserIDs) || !reflect.DeepEqual(syntheticACL.AllowedChannelIDs, cfg.ListenerConfigs[0].AllowedChannelIDs) {
		t.Fatalf("raw reply ACL = %+v, root ACL = %+v", syntheticACL, cfg.ListenerConfigs[0])
	}
}

func TestStdinExplicitKeywordIsDeliveredWithoutQueue(t *testing.T) {
	cfg, resolved, rawReply := resolveStdinReplyTestConfig(t)
	stdinStore := &cmd.StdinStore{}
	runtime := buildCommandSet(cfg.commandConfigs, newRunnerFactory(), pubsub.NewSlackOutput(100).NewCommandOutput)
	queued := false
	router := cmd.NewConversationRouter(stdinStore, runtime, nil, newMainTestDispatcher(context.Background(), stdinStore, nil, func(*cmd.CommandInput) bool { queued = true; return true }), 1)
	conversation := cmd.ConversationID{ChannelID: "C123", RootTimestamp: "1"}
	if result, err := router.Accept(&cmd.CommandInput{Text: "agent", ConversationID: conversation, MessageID: cmd.MessageID{Timestamp: "1"}, AllowedCommandIndexes: []int{resolved.Index}}); err != nil || result != cmd.AcceptRouted {
		t.Fatalf("root Accept() = %v, %v", result, err)
	}
	queued = false
	reader, writer := io.Pipe()
	endpoint := cmd.NewInteractiveStdin(writer, "", func(error) {})
	implicitReply := cmd.NewCommand(cmd.CommandConfig{
		Index:         rawReply.Index,
		MatcherConfig: cmd.MatcherConfig{RawMatcherConfig: cmd.RawMatcherConfig{Keyword: "*"}},
		ParserConfig:  cmd.ParserConfig{InputBodyMode: cmd.InputBodyRawStdin},
		RunnerConfig:  cmd.RunnerConfig{RawRunnerConfig: cmd.RawRunnerConfig{Runner: cmd.RunnerStdinReply, Command: "stdin-reply"}},
		Dispatch:      cmd.DispatchRunner,
	}, cmd.NewStdinReplyRunner(), nil, pubsub.NewSlackOutput(100).NewCommandOutput(pubsub.ReplyConfig{}, 0))
	stdinStore.Lifecycle(conversation).StdinReady(endpoint, implicitReply)
	for _, indexes := range [][]int{{resolved.Index}, {}} {
		result, err := router.Accept(&cmd.CommandInput{Text: "retry", ConversationID: conversation, MessageID: cmd.MessageID{Timestamp: "2"}, AllowedCommandIndexes: indexes})
		if err != nil || result != cmd.AcceptIgnored || queued {
			t.Fatalf("reply candidates %v result = %v, err = %v, queued = %v", indexes, result, err, queued)
		}
	}
	result, err := router.Accept(&cmd.CommandInput{Text: "retry", ConversationID: conversation, MessageID: cmd.MessageID{Timestamp: "2"}, AllowedCommandIndexes: []int{rawReply.Index}})
	if err != nil || result != cmd.AcceptRouted || queued {
		t.Fatalf("stdin explicit keyword result = %v, err = %v, queued = %v", result, err, queued)
	}
	line, err := bufio.NewReader(reader).ReadString('\n')
	if err != nil || line != "retry\n" {
		t.Fatalf("stdin explicit keyword delivery = %q, err = %v", line, err)
	}
	endpoint.Close()
	_ = reader.Close()
}

func TestBuildCommandSetIgnoresConfiguredRepliesForOneshot(t *testing.T) {
	root := &RawCommandConfig{
		RawMatcherConfig: cmd.RawMatcherConfig{Keyword: "agent"},
		RawRunnerConfig:  cmd.RawRunnerConfig{Command: "agent"},
		Replies:          []*RawCommandConfig{{RawMatcherConfig: cmd.RawMatcherConfig{Keyword: "retry"}, RawRunnerConfig: cmd.RawRunnerConfig{Command: "retry"}}},
	}
	cfg := validTestConfig(root)
	if err := resolveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	if len(cfg.commandConfigs[0].Replies) != 0 || len(cfg.ListenerConfigs) != 1 {
		t.Fatalf("oneshot resolved replies/listener candidates = %d/%+v, want none", len(cfg.commandConfigs[0].Replies), cfg.ListenerConfigs)
	}
	runtime := buildCommandSet(cfg.commandConfigs, newRunnerFactory(), pubsub.NewSlackOutput(100).NewCommandOutput)
	queued := false
	router := cmd.NewConversationRouter(nil, runtime, nil, newMainTestDispatcher(context.Background(), nil, nil, func(*cmd.CommandInput) bool { queued = true; return true }), 1)
	conversation := cmd.ConversationID{ChannelID: "C", RootTimestamp: "1"}
	if result, err := router.Accept(&cmd.CommandInput{Text: "agent", ConversationID: conversation, MessageID: cmd.MessageID{Timestamp: "1"}, AllowedCommandIndexes: []int{0}}); err != nil || result != cmd.AcceptRouted {
		t.Fatalf("root Accept() = %v, %v", result, err)
	}
	queued = false
	result, err := router.Accept(&cmd.CommandInput{Text: "retry", ConversationID: conversation, MessageID: cmd.MessageID{Timestamp: "2"}, AllowedCommandIndexes: []int{1}})
	if err != nil || result != cmd.AcceptIgnored || queued {
		t.Fatalf("oneshot reply result = %v, err = %v, queued = %v", result, err, queued)
	}
}

func TestValidateOpenAccessUsesTopLevelAllowLists(t *testing.T) {
	makeCommand := func(keyword string, users []string, replies ...*RawCommandConfig) *RawCommandConfig {
		return &RawCommandConfig{RawMatcherConfig: cmd.RawMatcherConfig{Keyword: keyword}, RawRunnerConfig: cmd.RawRunnerConfig{Command: keyword}, RawListenerConfig: pubsub.RawListenerConfig{AllowedUserIDs: users}, Replies: replies}
	}
	tests := []struct {
		name    string
		config  *Config
		wantErr bool
	}{
		{name: "command restriction does not replace top-level unsafe gate", config: func() *Config {
			cfg := validTestConfig(makeCommand("one", []string{"U123"}))
			cfg.AllowedUserIDs = nil
			return cfg
		}(), wantErr: true},
		{name: "inherited top-level restriction", config: validTestConfig(makeCommand("one", nil))},
		{name: "no commands and no top-level restriction", config: &Config{PubSubConfig: PubSubConfig{SlackBotToken: "xoxb-test", SlackAppToken: "xapp-test"}, NumWorkers: 1}, wantErr: true},
		{name: "unsafe access permits empty top-level allowlists", config: &Config{PubSubConfig: PubSubConfig{SlackBotToken: "xoxb-test", SlackAppToken: "xapp-test", AllowUnsafeOpenAccess: true}, NumWorkers: 1}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := resolveConfig(tt.config)
			if tt.wantErr && err == nil {
				t.Fatal("resolveConfig() accepted configuration with open command access")
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("resolveConfig() error = %v", err)
			}
		})
	}
}

func TestResolveConfigRejectsACLExpansion(t *testing.T) {
	for _, tc := range []struct {
		name      string
		topIDs    []string
		rootIDs   []string
		replyIDs  []string
		wantError bool
	}{
		{name: "root narrows top-level list", rootIDs: []string{"U123"}},
		{name: "root cannot expand top-level list", rootIDs: []string{"U-other"}, wantError: true},
		{name: "reply narrows parent list", rootIDs: []string{"U123"}, replyIDs: []string{"U123"}},
		{name: "reply cannot expand parent list with another top-level ID", topIDs: []string{"U123", "U-other"}, rootIDs: []string{"U123"}, replyIDs: []string{"U-other"}, wantError: true},
		{name: "empty reply list inherits parent", rootIDs: []string{"U123"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reply := &RawCommandConfig{RawMatcherConfig: cmd.RawMatcherConfig{Keyword: "reply"}, RawRunnerConfig: cmd.RawRunnerConfig{Command: "reply"}, RawListenerConfig: pubsub.RawListenerConfig{AllowedUserIDs: tc.replyIDs}}
			root := &RawCommandConfig{RawMatcherConfig: cmd.RawMatcherConfig{Keyword: "run"}, RawRunnerConfig: cmd.RawRunnerConfig{Command: "run"}, Interaction: cmd.InteractionCommand, RawListenerConfig: pubsub.RawListenerConfig{AllowedUserIDs: tc.rootIDs}, Replies: []*RawCommandConfig{reply}}
			cfg := validTestConfig(root)
			if tc.topIDs != nil {
				cfg.AllowedUserIDs = tc.topIDs
			}
			err := resolveConfig(cfg)
			if tc.wantError {
				if err == nil || !strings.Contains(err.Error(), "not allowed by its parent") {
					t.Fatalf("resolveConfig() error = %v, want parent ACL error", err)
				}
				prefix := "command keyword 'run': "
				if len(tc.replyIDs) > 0 {
					prefix = "reply keyword 'reply': "
				}
				if !strings.HasPrefix(err.Error(), "invalid configuration:\n  - "+prefix) || !strings.Contains(err.Error(), "is not allowed by its parent") {
					t.Fatalf("unexpected ACL error format: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("resolveConfig() error = %v", err)
			}
			if !reflect.DeepEqual(cfg.ListenerConfigs[0].AllowedUserIDs, []string{"U123"}) || !reflect.DeepEqual(cfg.ListenerConfigs[1].AllowedUserIDs, []string{"U123"}) {
				t.Fatalf("resolved users = %v", cfg.ListenerConfigs)
			}
		})
	}
}

func TestResolveConfigRejectsChannelACLExpansion(t *testing.T) {
	for _, tc := range []struct {
		name      string
		topIDs    []string
		rootIDs   []string
		replyIDs  []string
		wantError bool
	}{
		{name: "root cannot expand top-level channel list", rootIDs: []string{"C-other"}, wantError: true},
		{name: "reply cannot expand root channel list with another top-level ID", topIDs: []string{"C123", "C-other"}, rootIDs: []string{"C123"}, replyIDs: []string{"C-other"}, wantError: true},
		{name: "empty reply channel list inherits root", rootIDs: []string{"C123"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reply := &RawCommandConfig{RawMatcherConfig: cmd.RawMatcherConfig{Keyword: "reply"}, RawRunnerConfig: cmd.RawRunnerConfig{Command: "reply"}, RawListenerConfig: pubsub.RawListenerConfig{AllowedChannelIDs: tc.replyIDs}}
			root := &RawCommandConfig{RawMatcherConfig: cmd.RawMatcherConfig{Keyword: "run"}, RawRunnerConfig: cmd.RawRunnerConfig{Command: "run"}, Interaction: cmd.InteractionCommand, RawListenerConfig: pubsub.RawListenerConfig{AllowedChannelIDs: tc.rootIDs}, Replies: []*RawCommandConfig{reply}}
			cfg := validTestConfig(root)
			cfg.AllowedChannelIDs = []string{"C123"}
			if tc.topIDs != nil {
				cfg.AllowedChannelIDs = tc.topIDs
			}
			err := resolveConfig(cfg)
			if tc.wantError {
				if err == nil || !strings.Contains(err.Error(), "allowed_channel_ids") {
					t.Fatalf("resolveConfig() error = %v, want channel ACL error", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("resolveConfig() error = %v", err)
			}
			if !reflect.DeepEqual(cfg.ListenerConfigs[0].AllowedChannelIDs, []string{"C123"}) || !reflect.DeepEqual(cfg.ListenerConfigs[1].AllowedChannelIDs, []string{"C123"}) {
				t.Fatalf("resolved channels = %v", cfg.ListenerConfigs)
			}
		})
	}
}

func TestResolveConfigAllowsChildRestrictionOfUnrestrictedParent(t *testing.T) {
	reply := &RawCommandConfig{RawMatcherConfig: cmd.RawMatcherConfig{Keyword: "reply"}, RawRunnerConfig: cmd.RawRunnerConfig{Command: "reply"}, RawListenerConfig: pubsub.RawListenerConfig{AllowedUserIDs: []string{"U-other"}}}
	root := &RawCommandConfig{RawMatcherConfig: cmd.RawMatcherConfig{Keyword: "run"}, RawRunnerConfig: cmd.RawRunnerConfig{Command: "run"}, Interaction: cmd.InteractionCommand, Replies: []*RawCommandConfig{reply}}
	cfg := validTestConfig(root)
	cfg.AllowedUserIDs = nil
	cfg.AllowedChannelIDs = []string{"C123"}
	if err := resolveConfig(cfg); err != nil {
		t.Fatalf("resolveConfig() error = %v", err)
	}
	if len(cfg.ListenerConfigs[0].AllowedUserIDs) != 0 || !reflect.DeepEqual(cfg.ListenerConfigs[1].AllowedUserIDs, []string{"U-other"}) {
		t.Fatalf("resolved users = %v", cfg.ListenerConfigs)
	}
}

func decodeConfigString(text string, cfg *Config) error {
	return decodeConfig(strings.NewReader(text), cfg)
}

func writeConfigFile(t *testing.T, text string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func validTestConfig(commands ...*RawCommandConfig) *Config {
	return &Config{
		PubSubConfig: PubSubConfig{
			SlackBotToken:  "xoxb-test",
			SlackAppToken:  "xapp-test",
			AllowedUserIDs: []string{"U123"},
		},
		NumWorkers: 1,
		Commands:   commands,
	}
}

func TestResolveConfigCollectsTopLevelErrorsBeforeCommands(t *testing.T) {
	for _, users := range [][]string{nil, {"USLACKBOT"}} {
		t.Run(strings.Join(users, ","), func(t *testing.T) {
			negative := Duration(-time.Second)
			cfg := &Config{
				PubSubConfig:        PubSubConfig{AllowedUserIDs: users, ReplyConfig: pubsub.ReplyConfig{OutputFormat: "html"}},
				OutputFlushInterval: &negative,
				Commands:            []*RawCommandConfig{nil},
			}
			err := resolveConfig(cfg)
			if err == nil {
				t.Fatal("resolveConfig() accepted invalid top-level settings")
			}
			if !strings.HasPrefix(err.Error(), "invalid configuration:\n  - slack_bot_token is required\n  - slack_app_token is required\n") || strings.Contains(err.Error(), "command keyword") || strings.Contains(err.Error(), "reply keyword") {
				t.Fatalf("unexpected top-level error format: %v", err)
			}
			want := []string{"slack_bot_token is required", "slack_app_token is required", "num_workers must be >= 1 (got 0)", `unknown output_format "html"; valid values are "plain", "monospaced", and "markdown"`, "output_flush_interval must be >= 0"}
			if len(users) == 0 {
				want = append(want, "open access is disabled by default")
			} else {
				want = append(want, "USLACKBOT cannot be used in allowed_user_ids; use accept_reminder for Slack Reminder messages")
			}
			for _, message := range want {
				if !strings.Contains(err.Error(), message) {
					t.Errorf("resolveConfig() error = %v, want %q", err, message)
				}
			}
			if cfg.commandConfigs != nil || cfg.ListenerConfigs != nil {
				t.Fatal("command processing started despite top-level errors")
			}
		})
	}
}

func TestResolveConfigAllowedUserIDs(t *testing.T) {
	for _, id := range []string{"USLACKBOT", "U123", "B123"} {
		t.Run(id, func(t *testing.T) {
			cfg := validTestConfig()
			cfg.AllowedUserIDs = []string{id}
			err := resolveConfig(cfg)
			if id == "USLACKBOT" {
				want := "USLACKBOT cannot be used in allowed_user_ids; use accept_reminder for Slack Reminder messages"
				if err == nil || err.Error() != "invalid configuration:\n  - "+want {
					t.Fatalf("resolveConfig() error = %v, want %q", err, want)
				}
			} else if err != nil {
				t.Fatalf("resolveConfig() error = %v, want nil", err)
			}
		})
	}
}

func TestResolveConfigCollectsCommandErrorsWithoutPartialState(t *testing.T) {
	cfg := validTestConfig()
	if err := decodeConfigString(`
[[commands]]
keyword = "before"
command = "date"
[[commands]]
keyword = "bad-format"
output_format = "html"
timeout = "-1s"
[[commands]]
keyword = "bad-acl"
command = "date"
[[commands.replies]]
keyword = "bad-reply-acl"
command = "date"
allowed_user_ids = ["U-other"]
[[commands]]
keyword = "missing-command"
[[commands]]
keyword = "after"
command = "date"
`, cfg); err != nil {
		t.Fatal(err)
	}
	err := resolveConfig(cfg)
	want := []string{"invalid configuration:", `  - command keyword 'bad-format': unknown output_format "html"; valid values are "plain", "monospaced", and "markdown"`, `  - reply keyword 'bad-reply-acl': allowed_user_ids value "U-other" is not allowed by its parent`, `  - command keyword 'missing-command': command is required`}
	if err == nil {
		t.Fatal("resolveConfig() accepted invalid commands")
	}
	if got := strings.Split(err.Error(), "\n"); !reflect.DeepEqual(got, want) {
		t.Fatalf("resolveConfig() errors = %q, want %q", got, want)
	}
	if len(cfg.commandConfigs) != 2 || cfg.commandConfigs[0].Keyword != "before" || cfg.commandConfigs[1].Keyword != "after" || cfg.commandConfigs[1].Index != 1 {
		t.Fatalf("resolved commands = %+v, want before and after with consecutive indexes", cfg.commandConfigs)
	}
	wantListeners := []pubsub.ListenerConfig{
		{CommandIndex: 0, RawListenerConfig: pubsub.RawListenerConfig{AllowedUserIDs: []string{"U123"}}},
		{CommandIndex: 1, RawListenerConfig: pubsub.RawListenerConfig{AllowedUserIDs: []string{"U123"}}},
	}
	if !reflect.DeepEqual(cfg.ListenerConfigs, wantListeners) {
		t.Fatalf("listeners = %+v, want %+v", cfg.ListenerConfigs, wantListeners)
	}
}

func TestConfigErrorsIdentifyMissingKeywordsByPosition(t *testing.T) {
	for _, tc := range []struct {
		name string
		text string
		want string
	}{
		{name: "first command", text: "[[commands]]\ncommand = 'date'", want: "command #1: keyword is required"},
		{name: "second command after invalid command", text: "[[commands]]\nkeyword = 'foo'\n[[commands]]\ncommand = 'date'", want: "command #2: keyword is required"},
		{name: "blank command keyword", text: "[[commands]]\nkeyword = ' '\ncommand = 'date'", want: "command #1: keyword is required"},
		{name: "first reply", text: "[[commands]]\nkeyword = 'foo'\ncommand = 'date'\n[[commands.replies]]\ncommand = 'date'", want: "reply #1 of command keyword 'foo': keyword is required"},
		{name: "second reply", text: "[[commands]]\nkeyword = 'foo'\ncommand = 'date'\n[[commands.replies]]\nkeyword = 'bar'\ncommand = 'date'\n[[commands.replies]]\ncommand = 'date'", want: "reply #2 of command keyword 'foo': keyword is required"},
		{name: "blank reply keyword", text: "[[commands]]\nkeyword = 'foo'\ncommand = 'date'\n[[commands.replies]]\nkeyword = ' '\ncommand = 'date'", want: "reply #1 of command keyword 'foo': keyword is required"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := validTestConfig()
			if err := decodeConfigString(tc.text, cfg); err != nil {
				t.Fatal(err)
			}
			err := resolveConfig(cfg)
			if err == nil || !strings.Contains(err.Error(), "  - "+tc.want) || strings.Contains(err.Error(), "keyword ''") {
				t.Fatalf("resolveConfig() error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestConfigErrorEnumMessages(t *testing.T) {
	for _, tc := range []struct {
		name string
		raw  RawCommandConfig
		want string
	}{
		{name: "runner", raw: RawCommandConfig{RawRunnerConfig: cmd.RawRunnerConfig{Runner: "webhook"}}, want: `unknown runner "webhook"; valid values are "exec", "compose", and "http"`},
		{name: "interaction", raw: RawCommandConfig{Interaction: "session"}, want: `unknown interaction "session"; valid values are "oneshot", "stdin", and "command"`},
		{name: "output_format", raw: RawCommandConfig{ReplyConfig: pubsub.ReplyConfig{OutputFormat: "markdownx"}}, want: `unknown output_format "markdownx"; valid values are "plain", "monospaced", and "markdown"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.raw.Keyword = "notify"
			cfg := validTestConfig(&tc.raw)
			if err := resolveConfig(cfg); err == nil || err.Error() != "invalid configuration:\n  - command keyword 'notify': "+tc.want {
				t.Fatalf("resolveConfig() error = %v, want formatted command enum error", err)
			}
			if tc.name == "interaction" {
				return
			}
			tc.raw.Keyword = "*"
			cfg = validTestConfig(&RawCommandConfig{RawMatcherConfig: cmd.RawMatcherConfig{Keyword: "parent"}, RawRunnerConfig: cmd.RawRunnerConfig{Command: "date"}, Replies: []*RawCommandConfig{&tc.raw}})
			if err := resolveConfig(cfg); err == nil || err.Error() != "invalid configuration:\n  - reply keyword '*': "+tc.want {
				t.Fatalf("resolveConfig() error = %v, want formatted reply enum error", err)
			}
		})
	}
}

func TestValidateConfigRequiresSlackTokens(t *testing.T) {
	for _, tc := range []struct {
		name  string
		clear func(*Config)
		want  string
	}{
		{
			name:  "bot token",
			clear: func(cfg *Config) { cfg.SlackBotToken = " " },
			want:  "slack_bot_token is required",
		},
		{
			name:  "app token",
			clear: func(cfg *Config) { cfg.SlackAppToken = "" },
			want:  "slack_app_token is required",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := validTestConfig(&RawCommandConfig{RawMatcherConfig: cmd.RawMatcherConfig{Keyword: "date"}, RawRunnerConfig: cmd.RawRunnerConfig{Command: "date"}})
			tc.clear(cfg)

			err := resolveConfig(cfg)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("resolveConfig() error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestValidateConfigRequiresCommandFields(t *testing.T) {
	for _, tc := range []struct {
		name   string
		config func() *Config
		want   string
	}{
		{
			name: "top-level keyword is missing",
			config: func() *Config {
				return validTestConfig(&RawCommandConfig{RawRunnerConfig: cmd.RawRunnerConfig{Command: "date"}})
			},
			want: "keyword is required",
		},
		{
			name: "top-level keyword is blank",
			config: func() *Config {
				return validTestConfig(&RawCommandConfig{RawMatcherConfig: cmd.RawMatcherConfig{Keyword: " "}, RawRunnerConfig: cmd.RawRunnerConfig{Command: "date"}})
			},
			want: "keyword is required",
		},
		{
			name: "reply keyword is missing",
			config: func() *Config {
				return validTestConfig(&RawCommandConfig{
					RawMatcherConfig: cmd.RawMatcherConfig{Keyword: "date"}, RawRunnerConfig: cmd.RawRunnerConfig{Command: "date"},
					Replies: []*RawCommandConfig{{RawRunnerConfig: cmd.RawRunnerConfig{Command: "retry"}}},
				})
			},
			want: "keyword is required",
		},
		{
			name: "exec command is missing",
			config: func() *Config {
				return validTestConfig(&RawCommandConfig{RawMatcherConfig: cmd.RawMatcherConfig{Keyword: "date"}, RawRunnerConfig: cmd.RawRunnerConfig{Runner: cmd.RunnerExec}})
			},
			want: "command is required",
		},
		{
			name: "compose command is missing",
			config: func() *Config {
				return validTestConfig(&RawCommandConfig{RawMatcherConfig: cmd.RawMatcherConfig{Keyword: "date"}, RawRunnerConfig: cmd.RawRunnerConfig{Runner: cmd.RunnerCompose}})
			},
			want: "command is required",
		},
		{
			name: "reply exec command is missing",
			config: func() *Config {
				return validTestConfig(&RawCommandConfig{
					RawMatcherConfig: cmd.RawMatcherConfig{Keyword: "date"}, RawRunnerConfig: cmd.RawRunnerConfig{Command: "date"},
					Replies: []*RawCommandConfig{{RawMatcherConfig: cmd.RawMatcherConfig{Keyword: "retry"}}},
				})
			},
			want: "command is required",
		},
		{
			name: "reply compose command is missing",
			config: func() *Config {
				return validTestConfig(&RawCommandConfig{
					RawMatcherConfig: cmd.RawMatcherConfig{Keyword: "date"}, RawRunnerConfig: cmd.RawRunnerConfig{Command: "date"},
					Replies: []*RawCommandConfig{{
						RawMatcherConfig: cmd.RawMatcherConfig{Keyword: "retry"}, RawRunnerConfig: cmd.RawRunnerConfig{Runner: cmd.RunnerCompose},
					}},
				})
			},
			want: "command is required",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := resolveConfig(tc.config())
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("resolveConfig() error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestValidateConfigTimeout(t *testing.T) {
	for _, tc := range []struct {
		name    string
		timeout Duration
		wantErr string
	}{
		{name: "negative is rejected", timeout: -1, wantErr: "timeout must be >= 0"},
		{name: "zero is allowed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := validTestConfig(&RawCommandConfig{RawMatcherConfig: cmd.RawMatcherConfig{Keyword: "date"}, RawRunnerConfig: cmd.RawRunnerConfig{Command: "date"}, RawExecutorConfig: RawExecutorConfig{Timeout: &tc.timeout}})

			err := resolveConfig(cfg)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("resolveConfig() error = %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("resolveConfig() error = %v, want %q", err, tc.wantErr)
			}
		})
	}
}

func TestDecodeConfigExecutorDurations(t *testing.T) {
	for _, tc := range []struct {
		input string
		want  time.Duration
	}{
		{input: `timeout = "1m"`, want: time.Minute},
		{input: `timeout = 60000000000`, want: time.Minute},
		{input: `stdin_idle_timeout = "30s"`, want: 30 * time.Second},
		{input: `stdin_idle_timeout = 30000000000`, want: 30 * time.Second},
		{input: `timeout = "0s"`, want: 0},
		{input: `timeout = 0`, want: 0},
		{input: `stdin_idle_timeout = "0s"`, want: 0},
		{input: `stdin_idle_timeout = 0`, want: 0},
	} {
		t.Run(tc.input, func(t *testing.T) {
			var cfg Config
			if err := decodeConfigString("[[commands]]\n"+tc.input, &cfg); err != nil {
				t.Fatalf("decodeConfigString() error = %v", err)
			}
			var got *Duration
			if strings.HasPrefix(tc.input, "timeout") {
				got = cfg.Commands[0].Timeout
			} else {
				got = cfg.Commands[0].StdinIdleTimeout
			}
			if got == nil || time.Duration(*got) != tc.want {
				t.Fatalf("decoded duration = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestNegativeExecutorDurationsRejected(t *testing.T) {
	for _, tc := range []struct {
		name, key, want string
	}{
		{name: "timeout string negative", key: "timeout = \"-1ns\"", want: "timeout must be >= 0"},
		{name: "timeout integer negative", key: "timeout = -1", want: "timeout must be >= 0"},
		{name: "idle string negative", key: "stdin_idle_timeout = \"-1ns\"", want: "stdin_idle_timeout must be >= 0"},
		{name: "idle integer negative", key: "stdin_idle_timeout = -1", want: "stdin_idle_timeout must be >= 0"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var decoded Config
			input := fmt.Sprintf("[[commands]]\nkeyword = \"date\"\ncommand = \"date\"\n%s", tc.key)
			if err := decodeConfigString(input, &decoded); err != nil {
				t.Fatalf("decodeConfigString() error = %v", err)
			}
			cfg := validTestConfig(decoded.Commands[0])
			err := resolveConfig(cfg)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("resolveConfig() error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestExecutorDurationsMinimumResolution(t *testing.T) {
	for _, tc := range []struct {
		name, key, value, wantErr string
	}{
		{name: "timeout string below minimum", key: "timeout", value: `"999us"`, wantErr: "timeout must be 0 or at least 1ms"},
		{name: "timeout integer below minimum", key: "timeout", value: "999999", wantErr: "timeout must be 0 or at least 1ms"},
		{name: "idle string below minimum", key: "stdin_idle_timeout", value: `"999us"`, wantErr: "stdin_idle_timeout must be 0 or at least 1ms"},
		{name: "idle integer below minimum", key: "stdin_idle_timeout", value: "999999", wantErr: "stdin_idle_timeout must be 0 or at least 1ms"},
		{name: "timeout old seconds is below minimum", key: "timeout", value: "60", wantErr: "timeout must be 0 or at least 1ms"},
		{name: "timeout string minimum accepted", key: "timeout", value: `"1ms"`},
		{name: "timeout integer minimum accepted", key: "timeout", value: "1000000"},
		{name: "idle string minimum accepted", key: "stdin_idle_timeout", value: `"1ms"`},
		{name: "idle integer minimum accepted", key: "stdin_idle_timeout", value: "1000000"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var decoded Config
			input := fmt.Sprintf("[[commands]]\nkeyword = \"date\"\ncommand = \"date\"\n%s = %s", tc.key, tc.value)
			if err := decodeConfigString(input, &decoded); err != nil {
				t.Fatalf("decodeConfigString() error = %v", err)
			}
			err := resolveConfig(validTestConfig(decoded.Commands[0]))
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("resolveConfig() error = %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("resolveConfig() error = %v, want %q", err, tc.wantErr)
			}
		})
	}
}

func TestLoadConfigResolvesConfiguration(t *testing.T) {
	path := writeConfigFile(t, `
slack_bot_token = "xoxb-test"
slack_app_token = "xapp-test"
allowed_user_ids = ["U123"]

[[commands]]
keyword = "date"
command = "date"

[[commands]]
keyword = "notify"
runner = "http"
url = "https://example.com/notify"
output_format = "markdown"

[[commands.replies]]
keyword = "retry"
url = "https://example.com/retry"
`)

	cfg, err := loadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.NumWorkers != 1 {
		t.Fatalf("num_workers = %d, want 1", cfg.NumWorkers)
	}
	if len(cfg.commandConfigs[0].Replies) != 0 || cfg.commandConfigs[0].Runner != cmd.RunnerExec {
		t.Fatalf("resolved default command = %+v", cfg.commandConfigs[0])
	}
	if cfg.Commands[1].Replies[0].URL != "https://example.com/retry" {
		t.Fatalf("reply URL = %q", cfg.Commands[1].Replies[0].URL)
	}
}

func TestLoadConfigRejectsInvalidConfiguration(t *testing.T) {
	for _, tc := range []struct {
		name string
		text string
	}{
		{name: "unknown runner", text: "slack_bot_token = 'xoxb-test'\nslack_app_token = 'xapp-test'\nallowed_user_ids = ['U']\n[[commands]]\nkeyword = 'date'\ncommand = 'date'\nrunner = 'remote'"},
		{name: "invalid output format", text: "slack_bot_token = 'xoxb-test'\nslack_app_token = 'xapp-test'\nallowed_user_ids = ['U']\n[[commands]]\nkeyword = 'date'\ncommand = 'date'\noutput_format = 'html'"},
		{name: "invalid worker count", text: "slack_bot_token = 'xoxb-test'\nslack_app_token = 'xapp-test'\nallowed_user_ids = ['U']\nnum_workers = 0"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := loadConfig(writeConfigFile(t, tc.text)); err == nil {
				t.Fatal("loadConfig() accepted invalid configuration")
			}
		})
	}
}

func TestOutputFlushIntervalConfiguration(t *testing.T) {
	t.Run("defaults to one second", func(t *testing.T) {
		cfg, err := loadConfig(writeConfigFile(t, `
slack_bot_token = "xoxb-test"
slack_app_token = "xapp-test"
allowed_user_ids = ["U123"]

[[commands]]
keyword = "date"
command = "date"
`))
		if err != nil {
			t.Fatal(err)
		}
		if cfg.Commands[0].OutputFlushInterval != nil {
			t.Fatalf("output flush interval = %v, want unset", cfg.Commands[0].OutputFlushInterval)
		}
	})

	t.Run("accepts sub-millisecond interval", func(t *testing.T) {
		cfg := validTestConfig(&RawCommandConfig{
			RawMatcherConfig: cmd.RawMatcherConfig{Keyword: "date"},
			RawRunnerConfig:  cmd.RawRunnerConfig{Command: "date"},
		})
		interval := Duration(time.Nanosecond)
		cfg.OutputFlushInterval = &interval
		if err := resolveConfig(cfg); err != nil {
			t.Fatalf("resolveConfig() error = %v", err)
		}
	})

	t.Run("command and reply override inherited interval", func(t *testing.T) {
		cfg, err := loadConfig(writeConfigFile(t, `
slack_bot_token = "xoxb-test"
slack_app_token = "xapp-test"
allowed_user_ids = ["U123"]
output_flush_interval = "500ms"

[[commands]]
keyword = "date"
command = "date"

[[commands]]
keyword = "todo"
command = "todo"
output_flush_interval = "2s"

[[commands.replies]]
keyword = "retry"
command = "todo retry"

[[commands.replies]]
keyword = "stop"
command = "todo stop"
output_flush_interval = "0s"
`))
		if err != nil {
			t.Fatal(err)
		}
		if cfg.Commands[0].OutputFlushInterval != nil {
			t.Fatalf("top-level command output flush interval = %v, want unset", cfg.Commands[0].OutputFlushInterval)
		}
		if got := time.Duration(*cfg.Commands[1].OutputFlushInterval); got != 2*time.Second {
			t.Fatalf("command output flush interval = %s, want %s", got, 2*time.Second)
		}
		if cfg.Commands[1].Replies[0].OutputFlushInterval != nil {
			t.Fatalf("inherited reply output flush interval = %v, want unset", cfg.Commands[1].Replies[0].OutputFlushInterval)
		}
		if got := time.Duration(*cfg.Commands[1].Replies[1].OutputFlushInterval); got != 0 {
			t.Fatalf("overridden reply output flush interval = %s, want 0", got)
		}
	})

	t.Run("rejects negative interval", func(t *testing.T) {
		_, err := loadConfig(writeConfigFile(t, `
slack_bot_token = "xoxb-test"
slack_app_token = "xapp-test"
allowed_user_ids = ["U123"]
output_flush_interval = "-1s"

[[commands]]
keyword = "date"
command = "date"
`))
		if err == nil || !strings.Contains(err.Error(), "output_flush_interval must be >= 0") {
			t.Fatalf("loadConfig() error = %v, want negative interval error", err)
		}
	})

	t.Run("rejects invalid duration", func(t *testing.T) {
		var cfg Config
		err := decodeConfigString(`output_flush_interval = "fast"`, &cfg)
		if err == nil {
			t.Fatal("decodeConfig() accepted invalid duration")
		}
	})
}

func TestLoadConfigResolvesInheritedCommandValues(t *testing.T) {
	cfg, err := loadConfig(writeConfigFile(t, `
slack_bot_token = "xoxb-test"
slack_app_token = "xapp-test"
allowed_user_ids = ["U123"]
output_flush_interval = "500ms"

[[commands]]
keyword = "todo"
command = "todo"
interaction = "command"
timeout = "30s"
tty = true
output_flush_interval = "2s"

[[commands.replies]]
keyword = "retry"
command = "todo retry"
`))
	if err != nil {
		t.Fatal(err)
	}
	root := cfg.commandConfigs[0]
	if root.OutputFlushInterval != 2*time.Second || root.AllowInChain {
		t.Fatalf("resolved root = %+v", root)
	}
	if got := root.Replies[0].OutputFlushInterval; got != 2*time.Second {
		t.Fatalf("inherited reply interval = %s, want 2s", got)
	}
	inherited := root.Replies[0]
	if inherited.ParserConfig != root.ParserConfig || inherited.ExecutorConfig != root.ExecutorConfig {
		t.Fatalf("inherited reply config = %+v, root = %+v", inherited, root)
	}
	if root.Timeout != 30*time.Second || inherited.Timeout != root.Timeout {
		t.Fatalf("resolved timeout root=%s reply=%s, want both 30s", root.Timeout, inherited.Timeout)
	}
}

func TestLoadConfigResolvesReplyOverrides(t *testing.T) {
	cfg, err := loadConfig(writeConfigFile(t, `
slack_bot_token = "xoxb-test"
slack_app_token = "xapp-test"
allowed_user_ids = ["U123"]

[[commands]]
keyword = "todo"
command = "todo"
interaction = "command"
timeout = "30s"
tty = true

[[commands.replies]]
keyword = "notify"
runner = " http "
url = "https://example.com/notify"
tty = false

[[commands.replies]]
keyword = "stop"
command = "todo stop"
timeout = "0s"
tty = false
`))
	if err != nil {
		t.Fatal(err)
	}
	root := cfg.commandConfigs[0]
	if reply := root.Replies[0]; reply.Runner != cmd.RunnerHTTP || reply.Method != "POST" {
		t.Fatalf("normalized reply runner = %+v", reply.RunnerConfig)
	}
	overridden := root.Replies[1]
	if overridden.Timeout != 0 || overridden.TTY {
		t.Fatalf("overridden reply executor config = %+v", overridden.ExecutorConfig)
	}
}

func TestLoadConfigResolvesReplyStdinIdleTimeoutZeroOverride(t *testing.T) {
	cfg, err := loadConfig(writeConfigFile(t, `
slack_bot_token = "xoxb-test"
slack_app_token = "xapp-test"
allowed_user_ids = ["U123"]

[[commands]]
keyword = "agent"
command = "agent"
interaction = "command"
stdin_idle_timeout = "30s"
timeout = "2m"

[[commands.replies]]
keyword = "stop"
command = "agent stop"
stdin_idle_timeout = "0s"
timeout = "0s"

[[commands.replies]]
keyword = "continue"
command = "agent continue"
stdin_idle_timeout = "45s"
timeout = "1m"

[[commands.replies]]
keyword = "inherit"
command = "agent inherit"
`))
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.commandConfigs[0].Replies[0].StdinIdleTimeout; got != 0 {
		t.Fatalf("reply stdin_idle_timeout = %d, want 0", got)
	}
	if got := cfg.commandConfigs[0].Replies[0].Timeout; got != 0 {
		t.Fatalf("reply timeout = %s, want explicit zero", got)
	}
	if got := cfg.commandConfigs[0].Replies[1].StdinIdleTimeout; got != 45*time.Second {
		t.Fatalf("reply stdin_idle_timeout override = %s, want 45s", got)
	}
	if got := cfg.commandConfigs[0].Replies[1].Timeout; got != time.Minute {
		t.Fatalf("reply timeout override = %s, want 1m", got)
	}
	if got := cfg.commandConfigs[0].Replies[2].Timeout; got != 2*time.Minute {
		t.Fatalf("inherited reply timeout = %s, want 2m", got)
	}
	if got := cfg.commandConfigs[0].Replies[2].StdinIdleTimeout; got != 30*time.Second {
		t.Fatalf("inherited reply stdin_idle_timeout = %s, want 30s", got)
	}
}

func TestNormalizeInteraction(t *testing.T) {
	for _, tc := range []struct {
		name    string
		value   string
		want    string
		wantErr string
	}{
		{name: "empty defaults to oneshot", want: cmd.InteractionOneshot},
		{name: "oneshot", value: cmd.InteractionOneshot, want: cmd.InteractionOneshot},
		{name: "stdin", value: cmd.InteractionStdin, want: cmd.InteractionStdin},
		{name: "command", value: cmd.InteractionCommand, want: cmd.InteractionCommand},
		{name: "unknown is rejected", value: "session", wantErr: `unknown interaction "session"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := normalizeInteraction(tc.value)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("normalizeInteraction() error = %v, want %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Fatalf("normalizeInteraction() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestValidateCommandConfigResolvesInteractionSemantics(t *testing.T) {
	tests := []struct {
		interaction string
		allow       bool
		liveStdin   bool
		bodyMode    cmd.InputBodyMode
	}{
		{cmd.InteractionOneshot, true, false, cmd.InputBodyStdin},
		{cmd.InteractionStdin, true, true, cmd.InputBodyStdin},
		{cmd.InteractionCommand, false, false, cmd.InputBodyArgument},
	}
	for _, tt := range tests {
		t.Run(tt.interaction, func(t *testing.T) {
			config := &RawCommandConfig{RawMatcherConfig: cmd.RawMatcherConfig{Keyword: "run"}, RawRunnerConfig: cmd.RawRunnerConfig{Command: "run"}, Interaction: tt.interaction}
			resolved, err := resolveCommandConfig(config, pubsub.DefaultOutputFlushInterval, 1)
			if err != nil {
				t.Fatal(err)
			}
			if resolved.ParserConfig != (cmd.ParserConfig{AllowInChain: tt.allow, InputBodyMode: tt.bodyMode}) || resolved.InteractiveStdin != tt.liveStdin {
				t.Fatalf("resolved config = %+v", resolved)
			}
		})
	}
}

func TestValidateConfigHTTPInteraction(t *testing.T) {
	for _, tc := range []struct {
		name        string
		interaction string
		wantErr     string
	}{
		{name: "command is allowed", interaction: cmd.InteractionCommand},
		{name: "stdin is rejected", interaction: cmd.InteractionStdin, wantErr: `command keyword 'notify': http runner does not support interaction "stdin"; use "oneshot" or "command"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &Config{
				PubSubConfig: PubSubConfig{
					SlackBotToken:  "xoxb-test",
					SlackAppToken:  "xapp-test",
					AllowedUserIDs: []string{"U123"},
				},
				NumWorkers: 1,
				Commands: []*RawCommandConfig{{
					RawMatcherConfig: cmd.RawMatcherConfig{Keyword: "notify"}, RawRunnerConfig: cmd.RawRunnerConfig{Runner: cmd.RunnerHTTP, URL: "https://example.com"},
					Interaction: tc.interaction,
				}},
			}

			err := resolveConfig(cfg)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("resolveConfig() error = %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("resolveConfig() error = %v, want %q", err, tc.wantErr)
			}
		})
	}
}

func TestValidateConfigKeywordWildcardCount(t *testing.T) {
	for _, tc := range []struct {
		name    string
		keyword string
		wantErr bool
	}{
		{name: "without wildcard", keyword: "foo"},
		{name: "with one wildcard", keyword: "foo *"},
		{name: "literal asterisk", keyword: "foo*"},
		{name: "unclosed quote", keyword: `foo "bar`},
		{name: "with two wildcards", keyword: "foo * bar *", wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &Config{
				PubSubConfig: PubSubConfig{SlackBotToken: "xoxb-test", SlackAppToken: "xapp-test", AllowedUserIDs: []string{"U123"}},
				NumWorkers:   1,
				Commands: []*RawCommandConfig{{
					RawMatcherConfig: cmd.RawMatcherConfig{Keyword: tc.keyword}, RawRunnerConfig: cmd.RawRunnerConfig{Command: "echo"},
				}},
			}
			err := resolveConfig(cfg)
			if tc.wantErr && err == nil {
				t.Fatal("resolveConfig() accepted multiple wildcards")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("resolveConfig() error = %v", err)
			}
		})
	}
}

func TestValidateConfigRejectsMultipleWildcardsInReplyKeyword(t *testing.T) {
	cfg := &Config{
		PubSubConfig: PubSubConfig{SlackBotToken: "xoxb-test", SlackAppToken: "xapp-test", AllowedUserIDs: []string{"U123"}},
		NumWorkers:   1,
		Commands: []*RawCommandConfig{{
			RawMatcherConfig: cmd.RawMatcherConfig{Keyword: "todo"}, RawRunnerConfig: cmd.RawRunnerConfig{Command: "todo"},
			Replies: []*RawCommandConfig{{
				RawMatcherConfig: cmd.RawMatcherConfig{Keyword: "update * again *"}, RawRunnerConfig: cmd.RawRunnerConfig{Command: "todo"},
			}},
		}},
	}
	if err := resolveConfig(cfg); err == nil {
		t.Fatal("resolveConfig() accepted multiple wildcards in a reply keyword")
	}
}

func TestValidateConfigTTY(t *testing.T) {
	tests := []struct {
		name    string
		runner  string
		idle    Duration
		wantErr string
	}{
		{name: "exec", runner: "exec"},
		{name: "compose", runner: "compose"},
		{name: "http", runner: "http", wantErr: "tty is not supported for http runner"},
		{
			name: "idle timeout", runner: "exec", idle: Duration(300 * time.Second),
			wantErr: "tty cannot be used with stdin_idle_timeout",
		},
		{name: "zero idle timeout", runner: "exec", idle: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			matcher := cmd.RawMatcherConfig{Keyword: "agent"}
			runner := cmd.RawRunnerConfig{Command: "cat", Runner: tt.runner}
			tty := true
			if tt.runner == "http" {
				runner.URL = "http://example.com/hook"
			}
			cfg := &Config{
				PubSubConfig: PubSubConfig{SlackBotToken: "xoxb-test", SlackAppToken: "xapp-test", AllowedUserIDs: []string{"U123"}},
				NumWorkers:   1,
				Commands:     []*RawCommandConfig{{RawMatcherConfig: matcher, RawRunnerConfig: runner, RawExecutorConfig: RawExecutorConfig{TTY: &tty, StdinIdleTimeout: &tt.idle}}},
			}
			err := resolveConfig(cfg)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("resolveConfig() error = %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("resolveConfig() error = %v, want %q", err, tt.wantErr)
			}
		})
	}
}

func TestValidateConfigStdinIdleTimeout(t *testing.T) {
	tests := []struct {
		name    string
		timeout Duration
		wantErr string
	}{
		{
			name:    "negative is rejected",
			timeout: -1,
			wantErr: "command keyword 'agent': stdin_idle_timeout must be >= 0",
		},
		{name: "zero is allowed"},
		{name: "positive is allowed", timeout: Duration(300 * time.Second)},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &Config{
				PubSubConfig: PubSubConfig{SlackBotToken: "xoxb-test", SlackAppToken: "xapp-test", AllowedUserIDs: []string{"U123"}},
				NumWorkers:   1,
				Commands:     []*RawCommandConfig{{RawMatcherConfig: cmd.RawMatcherConfig{Keyword: "agent"}, RawRunnerConfig: cmd.RawRunnerConfig{Command: "cat"}, RawExecutorConfig: RawExecutorConfig{StdinIdleTimeout: &tc.timeout}}},
			}

			err := resolveConfig(cfg)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("resolveConfig() error = %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("resolveConfig() error = %v, want %q", err, tc.wantErr)
			}
		})
	}
}

func TestHTTPRejectsStdinIdleTimeout(t *testing.T) {
	for _, tc := range []struct {
		name, fields, wantErr string
	}{
		{name: "root zero allowed", fields: `stdin_idle_timeout = "0s"`},
		{name: "reply zero allowed", fields: "[[commands.replies]]\nkeyword = \"notify\"\ncommand = \"notify\"\nurl = \"https://example.com/reply\"\nstdin_idle_timeout = \"0s\""},
		{name: "root positive rejected", fields: `stdin_idle_timeout = "1s"`, wantErr: "stdin_idle_timeout is not supported for http runner"},
		{name: "reply positive rejected", fields: "[[commands.replies]]\nkeyword = \"notify\"\ncommand = \"notify\"\nurl = \"https://example.com/reply\"\nstdin_idle_timeout = \"1s\"", wantErr: "stdin_idle_timeout is not supported for http runner"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input := "slack_bot_token = \"xoxb-test\"\nslack_app_token = \"xapp-test\"\nnum_workers = 1\nallowed_user_ids = [\"U123\"]\n\n[[commands]]\nkeyword = \"hook\"\nrunner = \"http\"\nurl = \"https://example.com/hook\"\ninteraction = \"command\"\n" + tc.fields
			var cfg Config
			if err := decodeConfigString(input, &cfg); err != nil {
				t.Fatalf("decodeConfigString() error = %v", err)
			}
			err := resolveConfig(&cfg)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("resolveConfig() error = %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("resolveConfig() error = %v, want %q", err, tc.wantErr)
			}
		})
	}
}

func TestValidateConfigRejectsOpenAccessByDefault(t *testing.T) {
	cfg := &Config{
		PubSubConfig: PubSubConfig{
			SlackBotToken: "xoxb-test",
			SlackAppToken: "xapp-test",
		},
		NumWorkers: 1,
		Commands: []*RawCommandConfig{
			{RawMatcherConfig: cmd.RawMatcherConfig{Keyword: "date"}, RawRunnerConfig: cmd.RawRunnerConfig{Command: "date"}},
		},
	}

	err := resolveConfig(cfg)
	if err == nil {
		t.Fatalf("expected error for open access config")
	}
}

func TestConfigDecodesReplyBroadcast(t *testing.T) {
	var cfg Config
	if err := decodeConfigString(`
allowed_user_ids = ["U123"]

[[commands]]
keyword = "default"
command = "date"

[[commands]]
keyword = "broadcast"
command = "date"
reply_broadcast = true

[[commands]]
keyword = "thread-only"
command = "date"
reply_broadcast = false
`, &cfg); err != nil {
		t.Fatalf("decodeConfig() error = %v", err)
	}

	if cfg.Commands[0].ReplyBroadcast != nil {
		t.Fatalf("default reply_broadcast = %v, want unset", *cfg.Commands[0].ReplyBroadcast)
	}
	if cfg.Commands[1].ReplyBroadcast == nil || !*cfg.Commands[1].ReplyBroadcast {
		t.Fatal("reply_broadcast = true was not decoded")
	}
	if cfg.Commands[2].ReplyBroadcast == nil || *cfg.Commands[2].ReplyBroadcast {
		t.Fatal("reply_broadcast = false was not decoded")
	}
}

func TestValidateConfigOutputFormat(t *testing.T) {
	for _, tc := range []struct {
		name         string
		outputFormat string
		reply        bool
		wantErr      string
	}{
		{name: "unspecified"},
		{name: "plain", outputFormat: "plain"},
		{name: "monospaced", outputFormat: "monospaced"},
		{name: "markdown", outputFormat: "markdown"},
		{name: "rejects unknown command format", outputFormat: "html", wantErr: `keyword 'date': unknown output_format "html"`},
		{name: "rejects unknown reply format", outputFormat: "html", reply: true, wantErr: `keyword 'reply': unknown output_format "html"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &Config{
				PubSubConfig: PubSubConfig{SlackBotToken: "xoxb-test", SlackAppToken: "xapp-test", AllowedUserIDs: []string{"U123"}},
				NumWorkers:   1,
				Commands: []*RawCommandConfig{{
					RawMatcherConfig: cmd.RawMatcherConfig{Keyword: "date"}, RawRunnerConfig: cmd.RawRunnerConfig{Command: "date"},
					ReplyConfig: pubsub.ReplyConfig{OutputFormat: tc.outputFormat},
				}},
			}
			if tc.reply {
				cfg.Commands[0].OutputFormat = ""
				cfg.Commands[0].Replies = []*RawCommandConfig{{
					RawMatcherConfig: cmd.RawMatcherConfig{Keyword: "reply"}, RawRunnerConfig: cmd.RawRunnerConfig{Command: "date"},
					ReplyConfig: pubsub.ReplyConfig{OutputFormat: tc.outputFormat},
				}}
			}

			err := resolveConfig(cfg)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("resolveConfig() error = %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("resolveConfig() error = %v, want %q", err, tc.wantErr)
			}
		})
	}
}

func TestResolveReplyCommand(t *testing.T) {
	var cfg Config
	err := decodeConfigString(`
allowed_user_ids = ["U123"]

[[commands]]
keyword = "todo *"
command = "todo-wrapper *"
runner = "compose"
timeout = "1h"
stdin_idle_timeout = "5m"
tty = true
username = "todo bot"
icon_emoji = ":memo:"
icon_url = "https://example.com/icon.png"
reply_broadcast = true
output_format = "markdown"

[[commands.replies]]
keyword = "cancel"
command = "todo-wrapper --cancel"

[[commands.replies]]
keyword = "stop"
command = "todo-wrapper --stop"
runner = "exec"
timeout = "0s"
stdin_idle_timeout = "0s"
tty = false
username = "stop bot"
icon_emoji = ":octagonal_sign:"
icon_url = "https://example.com/stop.png"
reply_broadcast = false
output_format = "plain"
`, &cfg)
	if err != nil {
		t.Fatal(err)
	}

	t.Run("inherits parent settings", func(t *testing.T) {
		replyConfig := resolveReplyConfig(cfg.Commands[0].ReplyConfig, cfg.Commands[0].Replies[0])
		broadcast := true
		wantReplyConfig := &pubsub.ReplyConfig{
			Username: "todo bot", IconEmoji: ":memo:", IconURL: "https://example.com/icon.png",
			ReplyBroadcast: &broadcast, OutputFormat: "markdown",
		}
		if !reflect.DeepEqual(replyConfig, wantReplyConfig) {
			t.Fatalf("reply config = %+v, want %+v", replyConfig, wantReplyConfig)
		}
	})

	t.Run("overrides parent including explicit zero values", func(t *testing.T) {
		replyConfig := resolveReplyConfig(cfg.Commands[0].ReplyConfig, cfg.Commands[0].Replies[1])
		broadcast := false
		wantReplyConfig := &pubsub.ReplyConfig{
			Username: "stop bot", IconEmoji: ":octagonal_sign:", IconURL: "https://example.com/stop.png",
			ReplyBroadcast: &broadcast, OutputFormat: "plain",
		}
		if !reflect.DeepEqual(replyConfig, wantReplyConfig) {
			t.Fatalf("reply config = %+v, want %+v", replyConfig, wantReplyConfig)
		}
	})
}

func TestConfigRejectsInteractionOnReplyCommand(t *testing.T) {
	var cfg Config
	err := decodeConfigString(`
allowed_user_ids = ["U123"]
slack_bot_token = "xoxb-test"
slack_app_token = "xapp-test"
num_workers = 1

[[commands]]
keyword = "todo"
command = "todo-wrapper"

[[commands.replies]]
keyword = "cancel"
command = "todo-wrapper --cancel"
interaction = "command"
	`, &cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := resolveConfig(&cfg); err == nil || !strings.Contains(err.Error(), "interaction is not allowed on reply commands") {
		t.Fatalf("resolveConfig() error = %v, want reply interaction error", err)
	}
}

func TestConfigRejectsNestedCommandReplies(t *testing.T) {
	var cfg Config
	err := decodeConfigString(`
allowed_user_ids = ["U123"]
slack_bot_token = "xoxb-test"
slack_app_token = "xapp-test"
num_workers = 1

[[commands]]
keyword = "todo"
command = "todo-wrapper"

[[commands.replies]]
keyword = "cancel"
command = "todo-wrapper --cancel"

[[commands.replies.replies]]
keyword = "again"
command = "todo-wrapper"
	`, &cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := resolveConfig(&cfg); err == nil || !strings.Contains(err.Error(), "nested replies are not supported") {
		t.Fatalf("resolveConfig() error = %v, want nested reply error", err)
	}
}

func TestValidateConfigValidatesCommandReplies(t *testing.T) {
	cfg := &Config{
		PubSubConfig: PubSubConfig{SlackBotToken: "xoxb-test", SlackAppToken: "xapp-test", AllowedUserIDs: []string{"U123"}},
		NumWorkers:   1,
		Commands: []*RawCommandConfig{{
			RawMatcherConfig: cmd.RawMatcherConfig{Keyword: "todo"}, RawRunnerConfig: cmd.RawRunnerConfig{Command: "todo-wrapper"},
			Replies: []*RawCommandConfig{{
				RawMatcherConfig: cmd.RawMatcherConfig{Keyword: "cancel"}, RawRunnerConfig: cmd.RawRunnerConfig{Runner: "http"},
			}},
		}},
	}
	if err := resolveConfig(cfg); err == nil || !strings.Contains(err.Error(), "url is required") {
		t.Fatalf("resolveConfig() error = %v, want missing reply URL", err)
	}
}

func TestValidateConfigAllowsRestrictedConfig(t *testing.T) {
	cfg := &Config{
		PubSubConfig: PubSubConfig{
			SlackBotToken:  "xoxb-test",
			SlackAppToken:  "xapp-test",
			AllowedUserIDs: []string{"U123"},
		},
		NumWorkers: 1,
		Commands: []*RawCommandConfig{
			{RawMatcherConfig: cmd.RawMatcherConfig{Keyword: "date"}, RawRunnerConfig: cmd.RawRunnerConfig{Command: "date"}},
		},
	}

	if err := resolveConfig(cfg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateConfigAllowsExplicitUnsafeOpenAccess(t *testing.T) {
	cfg := &Config{
		PubSubConfig: PubSubConfig{
			SlackBotToken:         "xoxb-test",
			SlackAppToken:         "xapp-test",
			AllowUnsafeOpenAccess: true,
		},
		NumWorkers: 1,
		Commands: []*RawCommandConfig{
			{RawMatcherConfig: cmd.RawMatcherConfig{Keyword: "date"}, RawRunnerConfig: cmd.RawRunnerConfig{Command: "date"}},
		},
	}

	if err := resolveConfig(cfg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateConfigAllowsHTTPRunner(t *testing.T) {
	cfg := &Config{
		PubSubConfig: PubSubConfig{
			SlackBotToken:  "xoxb-test",
			SlackAppToken:  "xapp-test",
			AllowedUserIDs: []string{"U123"},
		},
		NumWorkers: 1,
		Commands: []*RawCommandConfig{
			{
				RawMatcherConfig: cmd.RawMatcherConfig{Keyword: "notify *"},
				RawRunnerConfig:  cmd.RawRunnerConfig{Runner: "http", Method: "POST", URL: "http://example.com/hook", Body: `{"text":"*"}`},
			},
		},
	}

	if err := resolveConfig(cfg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.commandConfigs[0].Method != "POST" {
		t.Fatalf("expected resolved method POST, got %q", cfg.commandConfigs[0].Method)
	}
}

func TestValidateConfigRejectsHTTPRunnerWithoutURL(t *testing.T) {
	cfg := &Config{
		PubSubConfig: PubSubConfig{
			SlackBotToken:  "xoxb-test",
			SlackAppToken:  "xapp-test",
			AllowedUserIDs: []string{"U123"},
		},
		NumWorkers: 1,
		Commands: []*RawCommandConfig{
			{
				RawMatcherConfig: cmd.RawMatcherConfig{Keyword: "notify *"}, RawRunnerConfig: cmd.RawRunnerConfig{Runner: "http"},
			},
		},
	}

	if err := resolveConfig(cfg); err == nil {
		t.Fatalf("expected error for http runner without url")
	}
}

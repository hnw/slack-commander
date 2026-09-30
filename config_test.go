package main

import (
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
			for _, want := range tt.want {
				if !strings.Contains(err.Error(), want) {
					t.Fatalf("decodeConfig() error = %q, want it to contain %q", err, want)
				}
			}
		})
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
			cfg := validTestConfig(&RawCommandConfig{MatcherConfig: cmd.MatcherConfig{Keyword: "date"}, RunnerConfig: cmd.RunnerConfig{Command: "date"}})
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
				return validTestConfig(&RawCommandConfig{RunnerConfig: cmd.RunnerConfig{Command: "date"}})
			},
			want: "keyword is required",
		},
		{
			name: "top-level keyword is blank",
			config: func() *Config {
				return validTestConfig(&RawCommandConfig{MatcherConfig: cmd.MatcherConfig{Keyword: " "}, RunnerConfig: cmd.RunnerConfig{Command: "date"}})
			},
			want: "keyword is required",
		},
		{
			name: "reply keyword is missing",
			config: func() *Config {
				return validTestConfig(&RawCommandConfig{
					MatcherConfig: cmd.MatcherConfig{Keyword: "date"}, RunnerConfig: cmd.RunnerConfig{Command: "date"},
					Replies: []*RawReplyCommandConfig{{RunnerConfig: cmd.RunnerConfig{Command: "retry"}}},
				})
			},
			want: "keyword is required",
		},
		{
			name: "exec command is missing",
			config: func() *Config {
				return validTestConfig(&RawCommandConfig{MatcherConfig: cmd.MatcherConfig{Keyword: "date"}, RunnerConfig: cmd.RunnerConfig{Runner: cmd.RunnerExec}})
			},
			want: "command is required",
		},
		{
			name: "compose command is missing",
			config: func() *Config {
				return validTestConfig(&RawCommandConfig{MatcherConfig: cmd.MatcherConfig{Keyword: "date"}, RunnerConfig: cmd.RunnerConfig{Runner: cmd.RunnerCompose}})
			},
			want: "command is required",
		},
		{
			name: "reply exec command is missing",
			config: func() *Config {
				return validTestConfig(&RawCommandConfig{
					MatcherConfig: cmd.MatcherConfig{Keyword: "date"}, RunnerConfig: cmd.RunnerConfig{Command: "date"},
					Replies: []*RawReplyCommandConfig{{MatcherConfig: cmd.MatcherConfig{Keyword: "retry"}}},
				})
			},
			want: "command is required",
		},
		{
			name: "reply compose command is missing",
			config: func() *Config {
				return validTestConfig(&RawCommandConfig{
					MatcherConfig: cmd.MatcherConfig{Keyword: "date"}, RunnerConfig: cmd.RunnerConfig{Command: "date"},
					Replies: []*RawReplyCommandConfig{{
						MatcherConfig: cmd.MatcherConfig{Keyword: "retry"}, RunnerConfig: cmd.RunnerConfig{Runner: cmd.RunnerCompose},
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
		timeout int
		wantErr string
	}{
		{name: "negative is rejected", timeout: -1, wantErr: "timeout must be >= 0"},
		{name: "zero is allowed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := validTestConfig(&RawCommandConfig{MatcherConfig: cmd.MatcherConfig{Keyword: "date"}, RunnerConfig: cmd.RunnerConfig{Command: "date"}, ExecutorConfig: cmd.ExecutorConfig{Timeout: tc.timeout}})

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
	if cfg.commandConfigs[0].ThreadReplyMode != cmd.ThreadReplyIgnore || cfg.commandConfigs[0].Runner != cmd.RunnerExec {
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
timeout = 30
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
	if root.OutputFlushInterval != 2*time.Second || root.ThreadReplyMode != cmd.ThreadReplyCommand || root.AllowInChain {
		t.Fatalf("resolved root = %+v", root)
	}
	if got := root.Replies[0].OutputFlushInterval; got != 2*time.Second {
		t.Fatalf("inherited reply interval = %s, want 2s", got)
	}
	inherited := root.Replies[0]
	if inherited.AllowInChain != root.AllowInChain ||
		inherited.InteractiveStdin != root.InteractiveStdin ||
		inherited.InputBodyMode != root.InputBodyMode ||
		inherited.Timeout != root.Timeout ||
		inherited.StdinIdleTimeout != root.StdinIdleTimeout ||
		inherited.TTY != root.TTY {
		t.Fatalf("inherited reply executor config = %+v, root = %+v", inherited.ExecutorConfig, root.ExecutorConfig)
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
timeout = 30
tty = true

[[commands.replies]]
keyword = "notify"
runner = " http "
url = "https://example.com/notify"
tty = false

[[commands.replies]]
keyword = "stop"
command = "todo stop"
timeout = 0
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
stdin_idle_timeout = 30

[[commands.replies]]
keyword = "stop"
command = "agent stop"
stdin_idle_timeout = 0
`))
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.commandConfigs[0].Replies[0].StdinIdleTimeout; got != 0 {
		t.Fatalf("reply stdin_idle_timeout = %d, want 0", got)
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
		replyMode   cmd.ThreadReplyMode
	}{
		{cmd.InteractionOneshot, true, false, cmd.InputBodyStdin, cmd.ThreadReplyIgnore},
		{cmd.InteractionStdin, false, true, cmd.InputBodyStdin, cmd.ThreadReplyStdin},
		{cmd.InteractionCommand, false, false, cmd.InputBodyArgument, cmd.ThreadReplyCommand},
	}
	for _, tt := range tests {
		t.Run(tt.interaction, func(t *testing.T) {
			config := &RawCommandConfig{MatcherConfig: cmd.MatcherConfig{Keyword: "run"}, RunnerConfig: cmd.RunnerConfig{Command: "run"}, Interaction: tt.interaction}
			resolved, err := resolveCommandConfig(config, cmd.DefaultOutputFlushInterval)
			if err != nil {
				t.Fatal(err)
			}
			if resolved.AllowInChain != tt.allow || resolved.InteractiveStdin != tt.liveStdin || resolved.InputBodyMode != tt.bodyMode || resolved.ThreadReplyMode != tt.replyMode {
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
		{name: "stdin is rejected", interaction: cmd.InteractionStdin, wantErr: "does not support stdin"},
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
					MatcherConfig: cmd.MatcherConfig{Keyword: "notify"}, RunnerConfig: cmd.RunnerConfig{Runner: cmd.RunnerHTTP, URL: "https://example.com"},
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
					MatcherConfig: cmd.MatcherConfig{Keyword: tc.keyword}, RunnerConfig: cmd.RunnerConfig{Command: "echo"},
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
			MatcherConfig: cmd.MatcherConfig{Keyword: "todo"}, RunnerConfig: cmd.RunnerConfig{Command: "todo"},
			Replies: []*RawReplyCommandConfig{{
				MatcherConfig: cmd.MatcherConfig{Keyword: "update * again *"}, RunnerConfig: cmd.RunnerConfig{Command: "todo"},
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
		idle    int
		wantErr string
	}{
		{name: "exec", runner: "exec"},
		{name: "compose", runner: "compose"},
		{name: "http", runner: "http", wantErr: "tty is not supported for http runner"},
		{
			name: "idle timeout", runner: "exec", idle: 300,
			wantErr: "tty cannot be used with stdin_idle_timeout",
		},
		{name: "zero idle timeout", runner: "exec", idle: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			matcher := cmd.MatcherConfig{Keyword: "agent"}
			runner := cmd.RunnerConfig{Command: "cat", Runner: tt.runner}
			executor := cmd.ExecutorConfig{TTY: true, StdinIdleTimeout: tt.idle}
			if tt.runner == "http" {
				runner.URL = "http://example.com/hook"
			}
			cfg := &Config{
				PubSubConfig: PubSubConfig{SlackBotToken: "xoxb-test", SlackAppToken: "xapp-test", AllowedUserIDs: []string{"U123"}},
				NumWorkers:   1,
				Commands:     []*RawCommandConfig{{MatcherConfig: matcher, RunnerConfig: runner, ExecutorConfig: executor}},
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
		timeout int
		wantErr string
	}{
		{
			name:    "negative is rejected",
			timeout: -1,
			wantErr: "stdin_idle_timeout must be >= 0 for keyword 'agent'",
		},
		{name: "zero is allowed"},
		{name: "positive is allowed", timeout: 300},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &Config{
				PubSubConfig: PubSubConfig{SlackBotToken: "xoxb-test", SlackAppToken: "xapp-test", AllowedUserIDs: []string{"U123"}},
				NumWorkers:   1,
				Commands:     []*RawCommandConfig{{MatcherConfig: cmd.MatcherConfig{Keyword: "agent"}, RunnerConfig: cmd.RunnerConfig{Command: "cat"}, ExecutorConfig: cmd.ExecutorConfig{StdinIdleTimeout: tc.timeout}}},
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

func TestValidateConfigRejectsOpenAccessByDefault(t *testing.T) {
	cfg := &Config{
		PubSubConfig: PubSubConfig{
			SlackBotToken: "xoxb-test",
			SlackAppToken: "xapp-test",
		},
		NumWorkers: 1,
		Commands: []*RawCommandConfig{
			{MatcherConfig: cmd.MatcherConfig{Keyword: "date"}, RunnerConfig: cmd.RunnerConfig{Command: "date"}},
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
					MatcherConfig: cmd.MatcherConfig{Keyword: "date"}, RunnerConfig: cmd.RunnerConfig{Command: "date"},
					ReplyConfig: pubsub.ReplyConfig{OutputFormat: tc.outputFormat},
				}},
			}
			if tc.reply {
				cfg.Commands[0].OutputFormat = ""
				cfg.Commands[0].Replies = []*RawReplyCommandConfig{{
					MatcherConfig: cmd.MatcherConfig{Keyword: "reply"}, RunnerConfig: cmd.RunnerConfig{Command: "date"},
					OutputFormat: tc.outputFormat,
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
timeout = 3600
stdin_idle_timeout = 300
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
timeout = 0
stdin_idle_timeout = 0
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

func TestBuildCommandSetUsesResolvedReplyRunner(t *testing.T) {
	parent := &RawCommandConfig{
		MatcherConfig: cmd.MatcherConfig{Keyword: "root"},
		RunnerConfig:  cmd.RunnerConfig{Command: "root"},
		Replies: []*RawReplyCommandConfig{{
			MatcherConfig: cmd.MatcherConfig{Keyword: "reply"},
			RunnerConfig:  cmd.RunnerConfig{Runner: " HTTP ", URL: "https://example.com"},
		}},
	}
	resolved, err := resolveCommandConfig(parent, cmd.DefaultOutputFlushInterval)
	if err != nil {
		t.Fatal(err)
	}
	var configs []cmd.RunnerConfig
	buildCommandSet([]*cmd.CommandConfig{resolved}, func(config cmd.RunnerConfig) cmd.CommandRunner {
		configs = append(configs, config)
		return cmd.NewExecRunner()
	})
	if configs[0].Runner != cmd.RunnerHTTP {
		t.Fatalf("reply runner = %q, want %q", configs[0].Runner, cmd.RunnerHTTP)
	}
}

func TestConfigRejectsInteractionOnReplyCommand(t *testing.T) {
	var cfg Config
	err := decodeConfigString(`
allowed_user_ids = ["U123"]

[[commands]]
keyword = "todo"
command = "todo-wrapper"

[[commands.replies]]
keyword = "cancel"
command = "todo-wrapper --cancel"
interaction = "command"
`, &cfg)
	if err == nil {
		t.Fatal("reply command interaction was accepted")
	}
}

func TestConfigRejectsNestedCommandReplies(t *testing.T) {
	var cfg Config
	err := decodeConfigString(`
allowed_user_ids = ["U123"]

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
	if err == nil {
		t.Fatal("nested replies were accepted")
	}
}

func TestValidateConfigValidatesCommandReplies(t *testing.T) {
	cfg := &Config{
		PubSubConfig: PubSubConfig{SlackBotToken: "xoxb-test", SlackAppToken: "xapp-test", AllowedUserIDs: []string{"U123"}},
		NumWorkers:   1,
		Commands: []*RawCommandConfig{{
			MatcherConfig: cmd.MatcherConfig{Keyword: "todo"}, RunnerConfig: cmd.RunnerConfig{Command: "todo-wrapper"},
			Replies: []*RawReplyCommandConfig{{
				MatcherConfig: cmd.MatcherConfig{Keyword: "cancel"}, RunnerConfig: cmd.RunnerConfig{Runner: "http"},
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
			{MatcherConfig: cmd.MatcherConfig{Keyword: "date"}, RunnerConfig: cmd.RunnerConfig{Command: "date"}},
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
			{MatcherConfig: cmd.MatcherConfig{Keyword: "date"}, RunnerConfig: cmd.RunnerConfig{Command: "date"}},
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
				MatcherConfig: cmd.MatcherConfig{Keyword: "notify *"},
				RunnerConfig:  cmd.RunnerConfig{Runner: "http", Method: "POST", URL: "http://example.com/hook", Body: `{"text":"*"}`},
			},
		},
	}

	if err := resolveConfig(cfg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Commands[0].Method != "POST" {
		t.Fatalf("expected method POST, got %q", cfg.Commands[0].Method)
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
				MatcherConfig: cmd.MatcherConfig{Keyword: "notify *"}, RunnerConfig: cmd.RunnerConfig{Runner: "http"},
			},
		},
	}

	if err := resolveConfig(cfg); err == nil {
		t.Fatalf("expected error for http runner without url")
	}
}

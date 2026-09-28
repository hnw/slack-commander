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

func validTestConfig(commands ...*CommandConfig) *Config {
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
			cfg := validTestConfig(&CommandConfig{ExecutionConfig: cmd.ExecutionConfig{Keyword: "date", Command: "date"}})
			tc.clear(cfg)

			err := validateConfig(cfg)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("validateConfig() error = %v, want %q", err, tc.want)
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
				return validTestConfig(&CommandConfig{ExecutionConfig: cmd.ExecutionConfig{Command: "date"}})
			},
			want: "keyword is required",
		},
		{
			name: "top-level keyword is blank",
			config: func() *Config {
				return validTestConfig(&CommandConfig{ExecutionConfig: cmd.ExecutionConfig{Keyword: " ", Command: "date"}})
			},
			want: "keyword is required",
		},
		{
			name: "reply keyword is missing",
			config: func() *Config {
				return validTestConfig(&CommandConfig{
					ExecutionConfig: cmd.ExecutionConfig{Keyword: "date", Command: "date"},
					Replies:         []*ReplyCommandConfig{{ExecutionConfig: cmd.ExecutionConfig{Command: "retry"}}},
				})
			},
			want: "keyword is required",
		},
		{
			name: "exec command is missing",
			config: func() *Config {
				return validTestConfig(&CommandConfig{ExecutionConfig: cmd.ExecutionConfig{Keyword: "date", Runner: cmd.RunnerExec}})
			},
			want: "command is required",
		},
		{
			name: "compose command is missing",
			config: func() *Config {
				return validTestConfig(&CommandConfig{ExecutionConfig: cmd.ExecutionConfig{Keyword: "date", Runner: cmd.RunnerCompose}})
			},
			want: "command is required",
		},
		{
			name: "reply exec command is missing",
			config: func() *Config {
				return validTestConfig(&CommandConfig{
					ExecutionConfig: cmd.ExecutionConfig{Keyword: "date", Command: "date"},
					Replies:         []*ReplyCommandConfig{{ExecutionConfig: cmd.ExecutionConfig{Keyword: "retry"}}},
				})
			},
			want: "command is required",
		},
		{
			name: "reply compose command is missing",
			config: func() *Config {
				return validTestConfig(&CommandConfig{
					ExecutionConfig: cmd.ExecutionConfig{Keyword: "date", Command: "date"},
					Replies: []*ReplyCommandConfig{{
						ExecutionConfig: cmd.ExecutionConfig{Keyword: "retry"},
						Runner:          cmd.RunnerCompose,
					}},
				})
			},
			want: "command is required",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := validateConfig(tc.config())
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("validateConfig() error = %v, want %q", err, tc.want)
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
			cfg := validTestConfig(&CommandConfig{ExecutionConfig: cmd.ExecutionConfig{
				Keyword: "date", Command: "date", Timeout: tc.timeout,
			}})

			err := validateConfig(cfg)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("validateConfig() error = %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("validateConfig() error = %v, want %q", err, tc.wantErr)
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
	if cfg.Commands[0].Interaction != cmd.InteractionOneshot || cfg.Commands[0].Runner != cmd.RunnerExec {
		t.Fatalf("default command = %+v", cfg.Commands[0])
	}
	if cfg.Commands[1].Method != "POST" {
		t.Fatalf("http method = %q, want POST", cfg.Commands[1].Method)
	}
	reply := cfg.Commands[1].Replies[0]
	if reply.ExecutionConfig.Runner != cmd.RunnerHTTP || reply.ReplyConfig.OutputFormat != pubsub.OutputFormatMarkdown {
		t.Fatalf("resolved reply = %+v", reply)
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
		if got := cfg.Commands[0].ExecutionConfig.OutputFlushInterval; got != time.Second {
			t.Fatalf("output flush interval = %s, want %s", got, time.Second)
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
		if got := cfg.Commands[0].ExecutionConfig.OutputFlushInterval; got != 500*time.Millisecond {
			t.Fatalf("top-level output flush interval = %s, want %s", got, 500*time.Millisecond)
		}
		if got := cfg.Commands[1].ExecutionConfig.OutputFlushInterval; got != 2*time.Second {
			t.Fatalf("command output flush interval = %s, want %s", got, 2*time.Second)
		}
		if got := cfg.Commands[1].Replies[0].ExecutionConfig.OutputFlushInterval; got != 2*time.Second {
			t.Fatalf("inherited reply output flush interval = %s, want %s", got, 2*time.Second)
		}
		if got := cfg.Commands[1].Replies[1].ExecutionConfig.OutputFlushInterval; got != 0 {
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
			config := &CommandConfig{ExecutionConfig: cmd.ExecutionConfig{Keyword: "run", Command: "run"}, Interaction: tt.interaction}
			if err := validateCommandConfig(config, cmd.DefaultOutputFlushInterval); err != nil {
				t.Fatal(err)
			}
			if config.AllowInChain != tt.allow || config.InteractiveStdin != tt.liveStdin || config.InputBodyMode != tt.bodyMode || config.ThreadReplyMode != tt.replyMode {
				t.Fatalf("resolved config = %+v", config)
			}
		})
	}
}

func TestCommandConfigEmbedsExecutionConfig(t *testing.T) {
	config := CommandConfig{ExecutionConfig: cmd.ExecutionConfig{Keyword: "run", Command: "run"}}
	if config.Keyword != "run" || config.Command != "run" {
		t.Fatalf("execution config = %+v", config.ExecutionConfig)
	}
}

func TestValidateCommandConfigResolvesCommandReplySemantics(t *testing.T) {
	config := &CommandConfig{
		ExecutionConfig: cmd.ExecutionConfig{Keyword: "run", Command: "run"},
		Interaction:     cmd.InteractionCommand,
		Replies: []*ReplyCommandConfig{{
			ExecutionConfig: cmd.ExecutionConfig{Keyword: "reply *", Command: "reply *"},
		}},
	}
	if err := validateCommandConfig(config, cmd.DefaultOutputFlushInterval); err != nil {
		t.Fatal(err)
	}
	reply := config.Replies[0].ExecutionConfig
	if reply.AllowInChain || reply.InteractiveStdin || reply.InputBodyMode != cmd.InputBodyArgument {
		t.Fatalf("reply definition = %+v", reply)
	}
	converted := commandConfigs([]*CommandConfig{config})[0].Replies[0].ExecutionConfig
	if converted.AllowInChain || converted.InteractiveStdin || converted.InputBodyMode != cmd.InputBodyArgument {
		t.Fatalf("converted reply definition = %+v", converted)
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
				Commands: []*CommandConfig{{
					ExecutionConfig: cmd.ExecutionConfig{Keyword: "notify", Runner: cmd.RunnerHTTP, URL: "https://example.com"},
					Interaction:     tc.interaction,
				}},
			}

			err := validateConfig(cfg)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("validateConfig() error = %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("validateConfig() error = %v, want %q", err, tc.wantErr)
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
				Commands: []*CommandConfig{{
					ExecutionConfig: cmd.ExecutionConfig{Keyword: tc.keyword, Command: "echo"},
				}},
			}
			err := validateConfig(cfg)
			if tc.wantErr && err == nil {
				t.Fatal("validateConfig() accepted multiple wildcards")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("validateConfig() error = %v", err)
			}
		})
	}
}

func TestValidateConfigRejectsMultipleWildcardsInReplyKeyword(t *testing.T) {
	cfg := &Config{
		PubSubConfig: PubSubConfig{SlackBotToken: "xoxb-test", SlackAppToken: "xapp-test", AllowedUserIDs: []string{"U123"}},
		NumWorkers:   1,
		Commands: []*CommandConfig{{
			ExecutionConfig: cmd.ExecutionConfig{Keyword: "todo", Command: "todo"},
			Replies: []*ReplyCommandConfig{{
				ExecutionConfig: cmd.ExecutionConfig{Keyword: "update * again *", Command: "todo"},
			}},
		}},
	}
	if err := validateConfig(cfg); err == nil {
		t.Fatal("validateConfig() accepted multiple wildcards in a reply keyword")
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
			definition := cmd.ExecutionConfig{
				Keyword: "agent", Command: "cat", Runner: tt.runner, TTY: true, StdinIdleTimeout: tt.idle,
			}
			if tt.runner == "http" {
				definition.URL = "http://example.com/hook"
			}
			cfg := &Config{
				PubSubConfig: PubSubConfig{SlackBotToken: "xoxb-test", SlackAppToken: "xapp-test", AllowedUserIDs: []string{"U123"}},
				NumWorkers:   1,
				Commands:     []*CommandConfig{{ExecutionConfig: definition}},
			}
			err := validateConfig(cfg)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("validateConfig() error = %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("validateConfig() error = %v, want %q", err, tt.wantErr)
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
				Commands: []*CommandConfig{{ExecutionConfig: cmd.ExecutionConfig{
					Keyword:          "agent",
					Command:          "cat",
					StdinIdleTimeout: tc.timeout,
				}}},
			}

			err := validateConfig(cfg)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("validateConfig() error = %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("validateConfig() error = %v, want %q", err, tc.wantErr)
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
		Commands: []*CommandConfig{
			{ExecutionConfig: cmd.ExecutionConfig{Keyword: "date", Command: "date"}},
		},
	}

	err := validateConfig(cfg)
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
				Commands: []*CommandConfig{{
					ExecutionConfig: cmd.ExecutionConfig{Keyword: "date", Command: "date"},
					ReplyConfig:     pubsub.ReplyConfig{OutputFormat: tc.outputFormat},
				}},
			}
			if tc.reply {
				cfg.Commands[0].OutputFormat = ""
				cfg.Commands[0].Replies = []*ReplyCommandConfig{{
					ExecutionConfig: cmd.ExecutionConfig{Keyword: "reply", Command: "date"},
					OutputFormat:    tc.outputFormat,
				}}
			}

			err := validateConfig(cfg)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("validateConfig() error = %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("validateConfig() error = %v, want %q", err, tc.wantErr)
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
		definition, replyConfig := resolveReplyCommand(cfg.Commands[0], cfg.Commands[0].Replies[0])
		wantDefinition := &cmd.ExecutionConfig{
			Keyword: "cancel", Command: "todo-wrapper --cancel", Runner: "compose",
			Timeout: 3600, StdinIdleTimeout: 300, TTY: true,
		}
		wantDefinition.ReplyConfig = definition.ReplyConfig
		wantDefinition.SystemReplyConfig = definition.SystemReplyConfig
		if !reflect.DeepEqual(definition, wantDefinition) {
			t.Fatalf("definition = %+v, want %+v", definition, wantDefinition)
		}
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
		definition, replyConfig := resolveReplyCommand(cfg.Commands[0], cfg.Commands[0].Replies[1])
		wantDefinition := &cmd.ExecutionConfig{Keyword: "stop", Command: "todo-wrapper --stop", Runner: "exec"}
		wantDefinition.ReplyConfig = definition.ReplyConfig
		wantDefinition.SystemReplyConfig = definition.SystemReplyConfig
		if !reflect.DeepEqual(definition, wantDefinition) {
			t.Fatalf("definition = %+v, want %+v", definition, wantDefinition)
		}
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
		Commands: []*CommandConfig{{
			ExecutionConfig: cmd.ExecutionConfig{Keyword: "todo", Command: "todo-wrapper"},
			Replies: []*ReplyCommandConfig{{
				ExecutionConfig: cmd.ExecutionConfig{Keyword: "cancel"},
				Runner:          "http",
			}},
		}},
	}
	if err := validateConfig(cfg); err == nil || !strings.Contains(err.Error(), "url is required") {
		t.Fatalf("validateConfig() error = %v, want missing reply URL", err)
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
		Commands: []*CommandConfig{
			{ExecutionConfig: cmd.ExecutionConfig{Keyword: "date", Command: "date"}},
		},
	}

	if err := validateConfig(cfg); err != nil {
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
		Commands: []*CommandConfig{
			{ExecutionConfig: cmd.ExecutionConfig{Keyword: "date", Command: "date"}},
		},
	}

	if err := validateConfig(cfg); err != nil {
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
		Commands: []*CommandConfig{
			{
				ExecutionConfig: cmd.ExecutionConfig{
					Keyword: "notify *",
					Runner:  "http",
					Method:  "POST",
					URL:     "http://example.com/hook",
					Body:    `{"text":"*"}`,
				},
			},
		},
	}

	if err := validateConfig(cfg); err != nil {
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
		Commands: []*CommandConfig{
			{
				ExecutionConfig: cmd.ExecutionConfig{
					Keyword: "notify *",
					Runner:  "http",
				},
			},
		},
	}

	if err := validateConfig(cfg); err == nil {
		t.Fatalf("expected error for http runner without url")
	}
}

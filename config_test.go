package main

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/hnw/slack-commander/cmd"
	"github.com/hnw/slack-commander/pubsub"
)

func TestDecodeConfigRejectsInvalidTOML(t *testing.T) {
	tests := []struct {
		name string
		text string
	}{
		{name: "unknown top-level field", text: "unknown = true"},
		{name: "unknown command field", text: "[[commands]]\nunknown = true"},
		{name: "unknown reply field", text: "[[commands]]\n[[commands.replies]]\nunknown = true"},
		{name: "type mismatch", text: "num_workers = 'one'"},
		{name: "syntax error", text: "num_workers ="},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var cfg Config
			if err := decodeConfig(strings.NewReader(tt.text), &cfg); err == nil {
				t.Fatal("decodeConfig() accepted invalid configuration")
			}
		})
	}
}

func TestDecodeConfigReportsTOMLContext(t *testing.T) {
	for _, tc := range []struct {
		name string
		text string
		want []string
	}{
		{name: "unknown field", text: "unknown = true", want: []string{"unknown", "field", "1|"}},
		{name: "nested unknown field", text: "[[commands]]\nunknown = true", want: []string{"unknown", "field", "2|"}},
		{name: "syntax error", text: "num_workers =", want: []string{"num_workers", "1|"}},
		{name: "type mismatch", text: "num_workers = 'one'", want: []string{"num_workers", "1|"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var cfg Config
			err := decodeConfigString(tc.text, &cfg)
			if err == nil {
				t.Fatal("decodeConfig() accepted invalid configuration")
			}
			for _, want := range tc.want {
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
			cfg := validTestConfig(&CommandConfig{Definition: cmd.Definition{Keyword: "date", Command: "date"}})
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
				return validTestConfig(&CommandConfig{Definition: cmd.Definition{Command: "date"}})
			},
			want: "keyword is required",
		},
		{
			name: "top-level keyword is blank",
			config: func() *Config {
				return validTestConfig(&CommandConfig{Definition: cmd.Definition{Keyword: " ", Command: "date"}})
			},
			want: "keyword is required",
		},
		{
			name: "reply keyword is missing",
			config: func() *Config {
				return validTestConfig(&CommandConfig{
					Definition: cmd.Definition{Keyword: "date", Command: "date"},
					Replies:    []*ReplyCommandConfig{{Definition: cmd.Definition{Command: "retry"}}},
				})
			},
			want: "keyword is required",
		},
		{
			name: "exec command is missing",
			config: func() *Config {
				return validTestConfig(&CommandConfig{Definition: cmd.Definition{Keyword: "date", Runner: cmd.RunnerExec}})
			},
			want: "command is required",
		},
		{
			name: "compose command is missing",
			config: func() *Config {
				return validTestConfig(&CommandConfig{Definition: cmd.Definition{Keyword: "date", Runner: cmd.RunnerCompose}})
			},
			want: "command is required",
		},
		{
			name: "reply exec command is missing",
			config: func() *Config {
				return validTestConfig(&CommandConfig{
					Definition: cmd.Definition{Keyword: "date", Command: "date"},
					Replies:    []*ReplyCommandConfig{{Definition: cmd.Definition{Keyword: "retry"}}},
				})
			},
			want: "command is required",
		},
		{
			name: "reply compose command is missing",
			config: func() *Config {
				return validTestConfig(&CommandConfig{
					Definition: cmd.Definition{Keyword: "date", Command: "date"},
					Replies: []*ReplyCommandConfig{{
						Definition: cmd.Definition{Keyword: "retry"},
						Runner:     cmd.RunnerCompose,
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
			cfg := validTestConfig(&CommandConfig{Definition: cmd.Definition{
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
	if reply.Definition.Runner != cmd.RunnerHTTP || reply.ReplyConfig.OutputFormat != pubsub.OutputFormatMarkdown {
		t.Fatalf("resolved reply = %+v", reply)
	}
}

func TestLoadConfigRejectsInvalidConfiguration(t *testing.T) {
	for _, tc := range []struct {
		name string
		text string
	}{
		{name: "unknown field", text: "unknown = true"},
		{name: "type mismatch", text: "num_workers = 'one'"},
		{name: "syntax error", text: "num_workers ="},
		{name: "unknown runner", text: "slack_bot_token = 'xoxb-test'\nslack_app_token = 'xapp-test'\nallowed_user_ids = ['U']\n[[commands]]\nkeyword = 'date'\ncommand = 'date'\nrunner = 'remote'"},
		{name: "unknown interaction", text: "slack_bot_token = 'xoxb-test'\nslack_app_token = 'xapp-test'\nallowed_user_ids = ['U']\n[[commands]]\nkeyword = 'date'\ncommand = 'date'\ninteraction = 'session'"},
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

func TestConfigStdinIdleTimeout(t *testing.T) {
	for _, setting := range []string{"", "stdin_idle_timeout = 0", "stdin_idle_timeout = 300"} {
		var cfg Config
		err := decodeConfigString(
			"[[commands]]\nkeyword = 'agent'\ncommand = 'cat'\ntimeout = 3600\n"+setting,
			&cfg,
		)
		if err != nil {
			t.Fatal(err)
		}
		want := 0
		if strings.HasSuffix(setting, "300") {
			want = 300
		}
		if len(cfg.Commands) != 1 || cfg.Commands[0].StdinIdleTimeout != want ||
			cfg.Commands[0].Timeout != 3600 {
			t.Fatalf("config=%+v", cfg)
		}
	}
}

func TestConfigInteraction(t *testing.T) {
	for _, tc := range []struct {
		name        string
		setting     string
		want        string
		wantErrText string
	}{
		{name: "defaults to oneshot", want: cmd.InteractionOneshot},
		{name: "stdin", setting: "interaction = 'stdin'", want: cmd.InteractionStdin},
		{name: "command", setting: "interaction = 'command'", want: cmd.InteractionCommand},
		{name: "rejects unknown", setting: "interaction = 'session'", wantErrText: "unknown interaction"},
		{name: "http supports command", setting: "interaction = 'command'\nrunner = 'http'\nurl = 'https://example.com'", want: cmd.InteractionCommand},
		{name: "http rejects stdin", setting: "interaction = 'stdin'\nrunner = 'http'\nurl = 'https://example.com'", wantErrText: "does not support stdin"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var cfg Config
			err := decodeConfigString("slack_bot_token = 'xoxb-test'\nslack_app_token = 'xapp-test'\nallowed_user_ids = ['U']\nnum_workers = 1\n[[commands]]\nkeyword = 'agent'\ncommand = 'cat'\n"+tc.setting, &cfg)
			if err != nil {
				t.Fatal(err)
			}
			err = validateConfig(&cfg)
			if tc.wantErrText != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErrText) {
					t.Fatalf("validateConfig() error = %v, want %q", err, tc.wantErrText)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got := cfg.Commands[0].Interaction; got != tc.want {
				t.Fatalf("interaction = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestNormalizeInteraction(t *testing.T) {
	for _, tc := range []struct {
		value   string
		want    string
		wantErr string
	}{
		{want: cmd.InteractionOneshot},
		{value: cmd.InteractionOneshot, want: cmd.InteractionOneshot},
		{value: cmd.InteractionStdin, want: cmd.InteractionStdin},
		{value: cmd.InteractionCommand, want: cmd.InteractionCommand},
		{value: "session", wantErr: `unknown interaction "session"`},
	} {
		t.Run(tc.value, func(t *testing.T) {
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
					Definition: cmd.Definition{Keyword: tc.keyword, Command: "echo"},
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
			Definition: cmd.Definition{Keyword: "todo", Command: "todo"},
			Replies: []*ReplyCommandConfig{{
				Definition: cmd.Definition{Keyword: "update * again *", Command: "todo"},
			}},
		}},
	}
	if err := validateConfig(cfg); err == nil {
		t.Fatal("validateConfig() accepted multiple wildcards in a reply keyword")
	}
}

func TestConfigTTYDefaultsToFalse(t *testing.T) {
	var cfg Config
	if err := decodeConfigString("[[commands]]\nkeyword = 'agent'\ncommand = 'cat'", &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.Commands[0].TTY {
		t.Fatal("tty default = true, want false")
	}
}

func TestConfigDecodesTTY(t *testing.T) {
	var cfg Config
	if err := decodeConfigString(
		"[[commands]]\nkeyword = 'agent'\ncommand = 'agent'\ntty = true",
		&cfg,
	); err != nil {
		t.Fatal(err)
	}
	if !cfg.Commands[0].TTY {
		t.Fatal("tty = true was not decoded")
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
			definition := cmd.Definition{
				Keyword: "agent", Command: "cat", Runner: tt.runner, TTY: true, StdinIdleTimeout: tt.idle,
			}
			if tt.runner == "http" {
				definition.URL = "http://example.com/hook"
			}
			cfg := &Config{
				PubSubConfig: PubSubConfig{SlackBotToken: "xoxb-test", SlackAppToken: "xapp-test", AllowedUserIDs: []string{"U123"}},
				NumWorkers:   1,
				Commands:     []*CommandConfig{{Definition: definition}},
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
				Commands: []*CommandConfig{{Definition: cmd.Definition{
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
			{Definition: cmd.Definition{Keyword: "date", Command: "date"}},
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

func TestConfigDecodesOutputFormat(t *testing.T) {
	var cfg Config
	err := decodeConfigString(`
allowed_user_ids = ["U123"]

[[commands]]
keyword = "markdown"
command = "date"
output_format = "markdown"

[[commands.replies]]
keyword = "monospaced"
command = "date"
output_format = "monospaced"
`, &cfg)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Commands[0].OutputFormat != "markdown" {
		t.Fatalf("command output_format = %q, want markdown", cfg.Commands[0].OutputFormat)
	}
	if cfg.Commands[0].Replies[0].OutputFormat != "monospaced" {
		t.Fatalf("reply output_format = %q, want monospaced", cfg.Commands[0].Replies[0].OutputFormat)
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
					Definition:  cmd.Definition{Keyword: "date", Command: "date"},
					ReplyConfig: pubsub.ReplyConfig{OutputFormat: tc.outputFormat},
				}},
			}
			if tc.reply {
				cfg.Commands[0].OutputFormat = ""
				cfg.Commands[0].Replies = []*ReplyCommandConfig{{
					Definition:   cmd.Definition{Keyword: "reply", Command: "date"},
					OutputFormat: tc.outputFormat,
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

//nolint:gocyclo // This test keeps every decoded reply field visible in one configuration fixture.
func TestConfigDecodesCommandReplies(t *testing.T) {
	var cfg Config
	err := decodeConfigString(`
allowed_user_ids = ["U123"]

[[commands]]
keyword = "todo *"
command = "todo-wrapper *"

[[commands.replies]]
keyword = "cancel"
command = "todo-wrapper --cancel"
runner = "http"
timeout = 30
tty = false
stdin_idle_timeout = 10
method = "POST"
url = "https://example.com/todo"
headers = { Authorization = "Bearer token" }
body = '{"text":"*"}'
username = "todo bot"
icon_emoji = ":memo:"
icon_url = "https://example.com/icon.png"
reply_broadcast = true
`, &cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Commands) != 1 || len(cfg.Commands[0].Replies) != 1 {
		t.Fatalf("commands = %+v", cfg.Commands)
	}
	reply := cfg.Commands[0].Replies[0]
	if reply.Keyword != "cancel" || reply.Command != "todo-wrapper --cancel" ||
		reply.Runner != "http" || reply.Timeout == nil || *reply.Timeout != 30 ||
		reply.TTY == nil || *reply.TTY ||
		reply.StdinIdleTimeout == nil || *reply.StdinIdleTimeout != 10 || reply.Method != "POST" ||
		reply.URL != "https://example.com/todo" || reply.Headers["Authorization"] != "Bearer token" ||
		reply.Body != `{"text":"*"}` || reply.Username != "todo bot" ||
		reply.IconEmoji != ":memo:" || reply.IconURL != "https://example.com/icon.png" ||
		reply.ReplyBroadcast == nil || !*reply.ReplyBroadcast {
		t.Fatalf("reply = %+v", reply)
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
		wantDefinition := &cmd.Definition{
			Keyword: "cancel", Command: "todo-wrapper --cancel", Runner: "compose",
			Timeout: 3600, StdinIdleTimeout: 300, TTY: true,
		}
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
		wantDefinition := &cmd.Definition{Keyword: "stop", Command: "todo-wrapper --stop", Runner: "exec"}
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
			Definition: cmd.Definition{Keyword: "todo", Command: "todo-wrapper"},
			Replies: []*ReplyCommandConfig{{
				Definition: cmd.Definition{Keyword: "cancel"},
				Runner:     "http",
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
			{Definition: cmd.Definition{Keyword: "date", Command: "date"}},
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
			{Definition: cmd.Definition{Keyword: "date", Command: "date"}},
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
				Definition: cmd.Definition{
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
				Definition: cmd.Definition{
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

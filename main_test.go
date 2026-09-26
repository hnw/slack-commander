package main

import (
	"reflect"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
	"github.com/hnw/slack-commander/cmd"
	"github.com/hnw/slack-commander/pubsub"
)

func TestConfigStdinIdleTimeout(t *testing.T) {
	for _, setting := range []string{"", "stdin_idle_timeout = 0", "stdin_idle_timeout = 300"} {
		var cfg Config
		_, err := toml.Decode(
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
		want        cmd.Interaction
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
			_, err := toml.Decode("allowed_user_ids = ['U']\nnum_workers = 1\n[[commands]]\nkeyword = 'agent'\ncommand = 'cat'\n"+tc.setting, &cfg)
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

func TestConfigTTYDefaultsToFalse(t *testing.T) {
	var cfg Config
	if _, err := toml.Decode("[[commands]]\nkeyword = 'agent'\ncommand = 'cat'", &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.Commands[0].TTY {
		t.Fatal("tty default = true, want false")
	}
}

func TestConfigDecodesTTY(t *testing.T) {
	var cfg Config
	if _, err := toml.Decode(
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
				PubSubConfig: PubSubConfig{AllowedUserIDs: []string{"U123"}},
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
				PubSubConfig: PubSubConfig{AllowedUserIDs: []string{"U123"}},
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
	if _, err := toml.Decode(`
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
		t.Fatalf("toml.Decode() error = %v", err)
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
	_, err := toml.Decode(`
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
				PubSubConfig: PubSubConfig{AllowedUserIDs: []string{"U123"}},
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
	_, err := toml.Decode(`
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
	_, err := toml.Decode(`
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
	metadata, err := toml.Decode(`
allowed_user_ids = ["U123"]

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
	if err := validateTOMLMetadata(metadata); err == nil {
		t.Fatal("reply command interaction was accepted")
	}
}

func TestConfigRejectsNestedCommandReplies(t *testing.T) {
	var cfg Config
	metadata, err := toml.Decode(`
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
	if err != nil {
		t.Fatal(err)
	}
	if err := validateTOMLMetadata(metadata); err == nil {
		t.Fatal("nested replies were accepted")
	}
}

func TestValidateConfigValidatesCommandReplies(t *testing.T) {
	cfg := &Config{
		PubSubConfig: PubSubConfig{AllowedUserIDs: []string{"U123"}},
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

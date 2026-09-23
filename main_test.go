package main

import (
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
	"github.com/hnw/slack-commander/cmd"
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

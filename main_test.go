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

func TestValidateConfigNormalizesContinuation(t *testing.T) {
	tests := []struct {
		name         string
		continuation string
		want         string
	}{
		{name: "unset defaults to empty", want: ""},
		{name: "thread is accepted", continuation: "thread", want: "thread"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &Config{
				PubSubConfig: PubSubConfig{AllowedUserIDs: []string{"U123"}},
				NumWorkers:   1,
				Commands: []*CommandConfig{{Definition: cmd.Definition{
					Keyword:      "date",
					Command:      "date",
					Continuation: tc.continuation,
				}}},
			}

			if err := validateConfig(cfg); err != nil {
				t.Fatalf("validateConfig() error = %v", err)
			}
			if got := cfg.Commands[0].Continuation; got != tc.want {
				t.Fatalf("continuation = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestConfigDecodesThreadContinuation(t *testing.T) {
	var cfg Config
	if _, err := toml.Decode(`
allowed_user_ids = ["U123"]
num_workers = 1

[[commands]]
keyword = "agent *"
command = "agent *"
continuation = "thread"
`, &cfg); err != nil {
		t.Fatalf("toml.Decode() error = %v", err)
	}

	if got := cfg.Commands[0].Continuation; got != cmd.ContinuationThread {
		t.Fatalf("continuation = %q, want %q", got, cmd.ContinuationThread)
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

func TestValidateConfigRejectsUnsupportedContinuation(t *testing.T) {
	cfg := &Config{
		PubSubConfig: PubSubConfig{AllowedUserIDs: []string{"U123"}},
		NumWorkers:   1,
		Commands: []*CommandConfig{{Definition: cmd.Definition{
			Keyword:      "date",
			Command:      "date",
			Continuation: "async",
		}}},
	}

	err := validateConfig(cfg)
	if err == nil ||
		!strings.Contains(err.Error(), "unknown continuation 'async' for keyword 'date'") {
		t.Fatalf("validateConfig() error = %v", err)
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

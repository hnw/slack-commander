package main

import (
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
	"github.com/hnw/slack-commander/cmd"
	"github.com/hnw/slack-commander/pubsub"
)

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
		postAsReply  bool
	}{
		{name: "unset defaults to empty", want: ""},
		{
			name:         "thread accepts post as reply",
			continuation: "thread",
			want:         "thread",
			postAsReply:  true,
		},
		{name: "thread enables post as reply", continuation: "thread", want: "thread"},
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
				}, ReplyConfig: pubsub.ReplyConfig{PostAsReply: tc.postAsReply}}},
			}

			if err := validateConfig(cfg); err != nil {
				t.Fatalf("validateConfig() error = %v", err)
			}
			if got := cfg.Commands[0].Continuation; got != tc.want {
				t.Fatalf("continuation = %q, want %q", got, tc.want)
			}
			wantPostAsReply := tc.postAsReply || tc.want == cmd.ContinuationThread
			if got := cfg.Commands[0].PostAsReply; got != wantPostAsReply {
				t.Fatalf("post_as_reply = %v, want %v", got, wantPostAsReply)
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

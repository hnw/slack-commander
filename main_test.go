package main

import (
	"testing"

	"github.com/hnw/slack-commander/cmd"
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

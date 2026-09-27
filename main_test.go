package main

import "testing"

func TestRunCheckConfig(t *testing.T) {
	if got := run([]string{"--help"}); got != 0 {
		t.Fatalf("run(--help) = %d, want 0", got)
	}

	valid := writeConfigFile(t, "slack_bot_token = 'xoxb-test'\nslack_app_token = 'xapp-test'\nallowed_user_ids = ['U']\n[[commands]]\nkeyword = 'date'\ncommand = 'date'")
	if got := run([]string{"--check-config", "--config-file", valid}); got != 0 {
		t.Fatalf("run(valid config) = %d, want 0", got)
	}

	missingBotToken := writeConfigFile(t, "slack_app_token = 'xapp-test'\nallowed_user_ids = ['U']\n[[commands]]\nkeyword = 'date'\ncommand = 'date'")
	if got := run([]string{"--check-config", "--config-file", missingBotToken}); got == 0 {
		t.Fatal("run(config without slack_bot_token) = 0, want non-zero")
	}

	invalid := writeConfigFile(t, "unknown = true")
	if got := run([]string{"--check-config", "--config-file", invalid}); got == 0 {
		t.Fatal("run(invalid config) = 0, want non-zero")
	}
}

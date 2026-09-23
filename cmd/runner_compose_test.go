package cmd

import (
	"bytes"
	"io"
	"testing"

	"github.com/hnw/compose-exec/compose"
)

func TestComposeStdinStartFailureDoesNotPublishWriter(t *testing.T) {
	c := &composeCmd{cmd: &compose.Cmd{}}
	var stderr bytes.Buffer
	c.SetStderr(&stderr)

	started := false
	if code := c.RunWithStdin(0, func(io.WriteCloser) { started = true }); code != 127 {
		t.Fatalf("code=%d", code)
	}
	if started {
		t.Fatal("published stdin after failed start")
	}
	_, err := io.ReadAll(c.cmd.Stdin)
	if err == nil {
		t.Fatal("stdin pipe remained open after start failure")
	}
}

func TestComposeCmdTTYEnablesComposeTTY(t *testing.T) {
	c := &composeCmd{cmd: &compose.Cmd{}}
	c.SetTTY()
	if !c.cmd.TTY {
		t.Fatal("compose TTY is disabled")
	}
}

func TestComposeCmdSetEnvOverridesExistingSlackContext(t *testing.T) {
	c := &composeCmd{cmd: &compose.Cmd{Env: []string{"SLACK_CHANNEL_ID=static"}}}
	c.SetEnv([]string{
		"SLACK_CHANNEL_ID=C123",
		"SLACK_THREAD_TS=1700000000.000100",
	})
	environment := c.cmd.Environ()
	if !containsEnvironment(environment, "SLACK_CHANNEL_ID=C123") {
		t.Fatalf("environment = %q", environment)
	}
	if containsEnvironment(environment, "SLACK_CHANNEL_ID=static") {
		t.Fatalf("static channel environment survived: %q", environment)
	}
	if !containsEnvironment(environment, "SLACK_THREAD_TS=1700000000.000100") {
		t.Fatalf("environment = %q", environment)
	}
}

func containsEnvironment(environment []string, want string) bool {
	for _, value := range environment {
		if value == want {
			return true
		}
	}
	return false
}

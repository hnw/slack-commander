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

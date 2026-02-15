package pubsub

import (
	"testing"
	"time"

	"github.com/hnw/slack-commander/cmd"
)

func TestEnqueueCommand(t *testing.T) {
	ch := make(chan *cmd.CommandInput, 1)
	input := &cmd.CommandInput{Text: "date"}

	if ok := enqueueCommand(ch, input); !ok {
		t.Fatalf("expected enqueue success")
	}
}

func TestEnqueueCommandQueueFullDoesNotBlock(t *testing.T) {
	ch := make(chan *cmd.CommandInput, 1)
	ch <- &cmd.CommandInput{Text: "filled"}

	start := time.Now()
	ok := enqueueCommand(ch, &cmd.CommandInput{Text: "drop-me"})
	if ok {
		t.Fatalf("expected enqueue failure when queue is full")
	}
	if d := time.Since(start); d > 100*time.Millisecond {
		t.Fatalf("enqueue blocked too long: %v", d)
	}
}

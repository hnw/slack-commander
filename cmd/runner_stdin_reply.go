package cmd

import (
	"context"
	"io"
	"log"
)

// StdinReplyRunner forwards stdin replies to the active stdin endpoint.
type StdinReplyRunner struct {
	store *StdinStore
}

// NewStdinReplyRunner creates a runner backed by the stdin endpoint store.
func NewStdinReplyRunner(store *StdinStore) CommandRunner {
	return &StdinReplyRunner{store: store}
}

// CommandContext receives ConversationID fields as runtime arguments used to locate the stdin endpoint; they are not argv for an external process.
func (r *StdinReplyRunner) CommandContext(_ context.Context, _ string, args ...string) Cmd {
	if len(args) != 2 {
		return &stdinReplyCmd{store: r.store}
	}
	return &stdinReplyCmd{
		store: r.store,
		conversation: ConversationID{
			ChannelID:     args[0],
			RootTimestamp: args[1],
		},
		valid: true,
	}
}

type stdinReplyCmd struct {
	store        *StdinStore
	conversation ConversationID
	valid        bool
	stdin        io.Reader
}

func (c *stdinReplyCmd) SetStdin(stdin io.Reader) { c.stdin = stdin }
func (*stdinReplyCmd) SetStdout(io.Writer)        {}
func (*stdinReplyCmd) SetStderr(io.Writer)        {}

func (c *stdinReplyCmd) Run(int) int {
	if !c.valid || c.store == nil || c.stdin == nil {
		return 127
	}
	body, err := io.ReadAll(c.stdin)
	if err != nil {
		log.Printf("[WARN] reading interactive stdin reply channel=%s thread=%s: %v", c.conversation.ChannelID, c.conversation.RootTimestamp, err)
		return 127
	}
	endpoint := c.store.lookup(c.conversation)
	if endpoint == nil {
		return 127
	}
	if err := endpoint.TrySend(string(body)); err != nil {
		log.Printf("[WARN] dropping interactive stdin channel=%s thread=%s: %v", c.conversation.ChannelID, c.conversation.RootTimestamp, err)
	}
	return 0
}

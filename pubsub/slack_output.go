package pubsub

import (
	"context"
	"time"

	"github.com/hnw/slack-commander/cmd"
	"github.com/slack-go/slack/socketmode"
)

// SlackOutput delivers output from multiple commands to a single Slack consumer.
type SlackOutput struct {
	events chan *slackOutputEvent
}

// NewSlackOutput creates an output pipeline with the given capacity.
func NewSlackOutput(capacity int) *SlackOutput {
	return &SlackOutput{events: make(chan *slackOutputEvent, capacity)}
}

// NewCommandOutput creates output with command-specific display settings and a flush interval.
func (o *SlackOutput) NewCommandOutput(reply ReplyConfig, interval time.Duration) cmd.CommandOutput {
	return &slackCommandOutput{queue: o.events, replyConfig: &reply, systemReplyConfig: NewSystemReplyConfig(reply.ReplyBroadcast), flushInterval: interval}
}

// Run must be started once. It drains output until Close,
// even after the context is canceled.
func (o *SlackOutput) Run(ctx context.Context, smc *socketmode.Client) {
	slackWriter(ctx, smc, o.events)
}

// Close must be called once, after all producers and final stream flushes have completed.
func (o *SlackOutput) Close() {
	close(o.events)
}

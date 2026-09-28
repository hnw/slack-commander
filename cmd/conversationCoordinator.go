package cmd

import (
	"log"
)

// RootTextResolver returns Slack-normalized text for a thread root.
type RootTextResolver func(ConversationContext) (string, error)

// StdinLifecycle receives process stdin availability notifications.
type StdinLifecycle interface {
	StdinReady(*InteractiveStdin)
	StdinClosed(*InteractiveStdin)
}

// ThreadReplyResult describes how a thread reply was handled.
type ThreadReplyResult int

const (
	// ThreadReplyIgnored means the reply did not have a routable destination.
	ThreadReplyIgnored ThreadReplyResult = iota
	// ThreadReplyRouted means the reply was sent to its selected destination.
	ThreadReplyRouted
	// ThreadReplyQueueFull means a command reply was dropped by the queue.
	ThreadReplyQueueFull
)

// ConversationCoordinator owns routing and execution state for Slack conversations.
type ConversationCoordinator struct {
	commands           []*CommandConfig
	resolve            RootTextResolver
	enqueue            func(*CommandInput) bool
	normalizeFirstLine func(string) string
	routes             *ThreadRouteCache
	inputs             ThreadInputRegistry
	locks              ThreadLocks
}

// NewConversationCoordinator creates the owner of conversation routing state.
func NewConversationCoordinator(commands []*CommandConfig, resolve RootTextResolver, enqueue func(*CommandInput) bool, normalizeFirstLine func(string) string, routeCapacity int) *ConversationCoordinator {
	return &ConversationCoordinator{commands: commands, resolve: resolve, enqueue: enqueue, normalizeFirstLine: normalizeFirstLine, routes: NewThreadRouteCache(routeCapacity)}
}

// AcceptRoot queues a root command and records its route after queue acceptance.
func (c *ConversationCoordinator) AcceptRoot(input *CommandInput) bool {
	root := MatchSingleCommand(input.Text, c.commands)
	if root != nil && root.Interaction == InteractionCommand {
		input.Text = c.normalizeCommandFirstLine(input.Text)
	}
	if c.enqueue == nil || !c.enqueue(input) {
		return false
	}
	if root != nil {
		c.routes.Store(input.ConversationContext.ThreadKey(), root)
	}
	return true
}

// AcceptThreadReply routes a reply according to its root command interaction.
func (c *ConversationCoordinator) AcceptThreadReply(input *CommandInput) (ThreadReplyResult, error) {
	root, found := c.routes.Lookup(input.ConversationContext.ThreadKey())
	if !found {
		if c.resolve == nil {
			return ThreadReplyIgnored, nil
		}
		text, err := c.resolve(input.ConversationContext)
		if err != nil {
			return ThreadReplyIgnored, err
		}
		root = MatchSingleCommand(text, c.commands)
		c.routes.Store(input.ConversationContext.ThreadKey(), root)
	}
	if root == nil {
		return ThreadReplyIgnored, nil
	}
	switch root.Interaction {
	case InteractionOneshot:
		return ThreadReplyIgnored, nil
	case InteractionStdin:
		endpoint := c.inputs.Lookup(input.ConversationContext.ThreadKey())
		if endpoint == nil {
			return ThreadReplyIgnored, nil
		}
		if err := endpoint.TrySend(input.Text); err != nil {
			log.Printf("[WARN] dropping interactive stdin channel=%s thread=%s: %v", input.ConversationContext.ChannelID, input.ConversationContext.RootThreadTimestamp, err)
		}
		return ThreadReplyRouted, nil
	case InteractionCommand:
		if len(root.Replies) == 0 || c.enqueue == nil {
			return ThreadReplyIgnored, nil
		}
		input.Text = c.normalizeCommandFirstLine(input.Text)
		input.CommandConfigs = root.Replies
		input.Interaction = InteractionCommand
		if !c.enqueue(input) {
			return ThreadReplyQueueFull, nil
		}
		return ThreadReplyRouted, nil
	}
	return ThreadReplyIgnored, nil
}

// RunSerialized runs work without overlapping commands in one conversation.
func (c *ConversationCoordinator) RunSerialized(ctx ConversationContext, run func()) {
	unlock := c.locks.Lock(ctx)
	defer unlock()
	run()
}

// Lifecycle returns a stdin lifecycle bound to one routable conversation.
func (c *ConversationCoordinator) Lifecycle(ctx ConversationContext) StdinLifecycle {
	if ctx.ChannelID == "" || ctx.RootThreadTimestamp == "" {
		return nil
	}
	return conversationLifecycle{coordinator: c, context: ctx}
}

type conversationLifecycle struct {
	coordinator *ConversationCoordinator
	context     ConversationContext
}

func (l conversationLifecycle) StdinReady(endpoint *InteractiveStdin) {
	l.coordinator.inputs.Register(l.context.ThreadKey(), endpoint)
}

func (l conversationLifecycle) StdinClosed(endpoint *InteractiveStdin) {
	l.coordinator.inputs.Unregister(l.context.ThreadKey(), endpoint)
}

func (c *ConversationCoordinator) normalizeCommandFirstLine(text string) string {
	if c.normalizeFirstLine == nil {
		return text
	}
	return c.normalizeFirstLine(text)
}

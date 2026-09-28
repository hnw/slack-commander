package cmd

import (
	"container/list"
	"log"
	"sync"
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
	routes             *conversationRoutes
	inputs             conversationInputs
	locks              conversationLocks
}

// CommandConfig holds application-level conversation directives.
type CommandConfig struct {
	*ExecutionConfig
	Replies         []*CommandConfig
	ThreadReplyMode ThreadReplyMode
}

// NewCommandConfig builds a runtime command config from resolved execution settings.
func NewCommandConfig(config *ExecutionConfig) *CommandConfig {
	return &CommandConfig{ExecutionConfig: config}
}

// MatchSingleCommand returns the configured command matching one complete input command.
// Chained or malformed inputs have no owner for thread reply routing.
func MatchSingleCommand(text string, configs []*CommandConfig) *CommandConfig {
	cmdMsg, _ := splitCommandInput(text)
	cmds, err := parseCommands(cmdMsg)
	if err != nil || len(cmds) != 1 {
		return nil
	}
	for _, config := range configs {
		matcher := newMatcher(config.ExecutionConfig)
		if matcher != nil && len(matcher.build(cmds[0].args)) > 0 {
			return config
		}
	}
	return nil
}

// NewConversationCoordinator creates the owner of conversation routing state.
func NewConversationCoordinator(commands []*CommandConfig, resolve RootTextResolver, enqueue func(*CommandInput) bool, normalizeFirstLine func(string) string, routeCapacity int) *ConversationCoordinator {
	return &ConversationCoordinator{commands: commands, resolve: resolve, enqueue: enqueue, normalizeFirstLine: normalizeFirstLine, routes: newConversationRoutes(routeCapacity)}
}

// AcceptRoot queues a root command and records its route after queue acceptance.
func (c *ConversationCoordinator) AcceptRoot(input *CommandInput) bool {
	return c.acceptRoot(input, input.Text)
}

// AcceptNormalizedRoot queues a root using Slack-normalized text for matching.
func (c *ConversationCoordinator) AcceptNormalizedRoot(input *CommandInput, normalizedText string) bool {
	return c.acceptRoot(input, normalizedText)
}

func (c *ConversationCoordinator) acceptRoot(input *CommandInput, normalizedText string) bool {
	root := MatchSingleCommand(normalizedText, c.commands)
	if root != nil && root.ThreadReplyMode == ThreadReplyCommand {
		input.Text = c.normalizeCommandFirstLine(input.Text)
	} else {
		input.Text = normalizedText
	}
	if c.enqueue == nil || !c.enqueue(input) {
		return false
	}
	if root != nil {
		c.routes.store(newConversationKey(input.ConversationContext), root)
	}
	return true
}

// AcceptThreadReply routes a reply according to its root command policy.
func (c *ConversationCoordinator) AcceptThreadReply(input *CommandInput) (ThreadReplyResult, error) {
	root, found := c.routes.lookup(newConversationKey(input.ConversationContext))
	if !found {
		if c.resolve == nil {
			return ThreadReplyIgnored, nil
		}
		text, err := c.resolve(input.ConversationContext)
		if err != nil {
			return ThreadReplyIgnored, err
		}
		root = MatchSingleCommand(text, c.commands)
		c.routes.store(newConversationKey(input.ConversationContext), root)
	}
	if root == nil {
		return ThreadReplyIgnored, nil
	}
	switch root.ThreadReplyMode {
	case ThreadReplyIgnore:
		return ThreadReplyIgnored, nil
	case ThreadReplyStdin:
		endpoint := c.inputs.lookup(newConversationKey(input.ConversationContext))
		if endpoint == nil {
			return ThreadReplyIgnored, nil
		}
		if err := endpoint.TrySend(input.Text); err != nil {
			log.Printf("[WARN] dropping interactive stdin channel=%s thread=%s: %v", input.ConversationContext.ChannelID, input.ConversationContext.RootThreadTimestamp, err)
		}
		return ThreadReplyRouted, nil
	case ThreadReplyCommand:
		if len(root.Replies) == 0 || c.enqueue == nil {
			return ThreadReplyIgnored, nil
		}
		input.Text = c.normalizeCommandFirstLine(input.Text)
		input.ExecutionConfigs = ExecutionConfigs(root.Replies)
		if !c.enqueue(input) {
			return ThreadReplyQueueFull, nil
		}
		return ThreadReplyRouted, nil
	}
	return ThreadReplyIgnored, nil
}

// ExecutionConfigs returns the resolved execution settings for runtime commands.
func ExecutionConfigs(commands []*CommandConfig) []*ExecutionConfig {
	configs := make([]*ExecutionConfig, len(commands))
	for i, command := range commands {
		configs[i] = command.ExecutionConfig
	}
	return configs
}

// RunSerialized runs work without overlapping commands in one conversation.
func (c *ConversationCoordinator) RunSerialized(ctx ConversationContext, run func()) {
	unlock := c.locks.lock(ctx)
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
	l.coordinator.inputs.register(newConversationKey(l.context), endpoint)
}

func (l conversationLifecycle) StdinClosed(endpoint *InteractiveStdin) {
	l.coordinator.inputs.unregister(newConversationKey(l.context), endpoint)
}

func (c *ConversationCoordinator) normalizeCommandFirstLine(text string) string {
	if c.normalizeFirstLine == nil {
		return text
	}
	return c.normalizeFirstLine(text)
}

type conversationKey struct {
	channelID           string
	rootThreadTimestamp string
}

func newConversationKey(context ConversationContext) conversationKey {
	return conversationKey{channelID: context.ChannelID, rootThreadTimestamp: context.RootThreadTimestamp}
}

type conversationInputs struct {
	mu     sync.Mutex
	inputs map[conversationKey]*InteractiveStdin
}

func (i *conversationInputs) register(key conversationKey, input *InteractiveStdin) {
	input.mu.Lock()
	defer input.mu.Unlock()
	if input.closed {
		return
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.inputs == nil {
		i.inputs = make(map[conversationKey]*InteractiveStdin)
	}
	i.inputs[key] = input
}

func (i *conversationInputs) lookup(key conversationKey) *InteractiveStdin {
	i.mu.Lock()
	defer i.mu.Unlock()
	return i.inputs[key]
}

func (i *conversationInputs) unregister(key conversationKey, input *InteractiveStdin) {
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.inputs[key] == input {
		delete(i.inputs, key)
	}
}

type conversationLocks struct {
	mu    sync.Mutex
	locks map[conversationKey]*sync.Mutex
}

func (l *conversationLocks) lock(context ConversationContext) func() {
	if context.ChannelID == "" || context.RootThreadTimestamp == "" {
		return func() {}
	}
	key := newConversationKey(context)
	l.mu.Lock()
	if l.locks == nil {
		l.locks = make(map[conversationKey]*sync.Mutex)
	}
	lock := l.locks[key]
	if lock == nil {
		lock = &sync.Mutex{}
		l.locks[key] = lock
	}
	l.mu.Unlock()
	lock.Lock()
	return lock.Unlock
}

type conversationRoutes struct {
	mu       sync.Mutex
	capacity int
	entries  map[conversationKey]*list.Element
	lru      *list.List
}

type conversationRoute struct {
	key     conversationKey
	command *CommandConfig
}

func newConversationRoutes(capacity int) *conversationRoutes {
	return &conversationRoutes{capacity: capacity}
}

func (r *conversationRoutes) lookup(key conversationKey) (*CommandConfig, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.entries == nil {
		return nil, false
	}
	element := r.entries[key]
	if element == nil {
		return nil, false
	}
	r.lru.MoveToFront(element)
	return element.Value.(conversationRoute).command, true
}

func (r *conversationRoutes) store(key conversationKey, command *CommandConfig) {
	if r.capacity <= 0 {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.entries == nil {
		r.entries = make(map[conversationKey]*list.Element)
		r.lru = list.New()
	}
	if element := r.entries[key]; element != nil {
		element.Value = conversationRoute{key: key, command: command}
		r.lru.MoveToFront(element)
		return
	}
	element := r.lru.PushFront(conversationRoute{key: key, command: command})
	r.entries[key] = element
	if r.lru.Len() <= r.capacity {
		return
	}
	oldest := r.lru.Back()
	delete(r.entries, oldest.Value.(conversationRoute).key)
	r.lru.Remove(oldest)
}

package cmd

import (
	"container/list"
	"log"
	"sync"
)

// RootTextResolver returns Slack-normalized text for a thread root.
type RootTextResolver func(ConversationID) (string, error)

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
	commands []*CommandConfig
	resolve  RootTextResolver
	enqueue  func(*CommandInput) bool
	routes   *conversationRoutes
	inputs   conversationInputs
	locks    conversationLocks
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
func NewConversationCoordinator(commands []*CommandConfig, resolve RootTextResolver, enqueue func(*CommandInput) bool, routeCapacity int) *ConversationCoordinator {
	return &ConversationCoordinator{commands: commands, resolve: resolve, enqueue: enqueue, routes: newConversationRoutes(routeCapacity)}
}

// AcceptRoot queues a root command and records its route after queue acceptance.
func (c *ConversationCoordinator) AcceptRoot(input *CommandInput) bool {
	root := MatchSingleCommand(input.Text, c.commands)
	if c.enqueue == nil || !c.enqueue(input) {
		return false
	}
	if root != nil {
		c.routes.store(input.ConversationID, root)
	}
	return true
}

// AcceptThreadReply routes a reply according to its root command policy.
func (c *ConversationCoordinator) AcceptThreadReply(input *CommandInput) (ThreadReplyResult, error) {
	root, found := c.routes.lookup(input.ConversationID)
	if !found {
		if c.resolve == nil {
			return ThreadReplyIgnored, nil
		}
		text, err := c.resolve(input.ConversationID)
		if err != nil {
			return ThreadReplyIgnored, err
		}
		root = MatchSingleCommand(text, c.commands)
		c.routes.store(input.ConversationID, root)
	}
	if root == nil {
		return ThreadReplyIgnored, nil
	}
	switch root.ThreadReplyMode {
	case ThreadReplyIgnore:
		return ThreadReplyIgnored, nil
	case ThreadReplyStdin:
		endpoint := c.inputs.lookup(input.ConversationID)
		if endpoint == nil {
			return ThreadReplyIgnored, nil
		}
		if err := endpoint.TrySend(input.Text); err != nil {
			log.Printf("[WARN] dropping interactive stdin channel=%s thread=%s: %v", input.ConversationID.ChannelID, input.ConversationID.RootTimestamp, err)
		}
		return ThreadReplyRouted, nil
	case ThreadReplyCommand:
		if len(root.Replies) == 0 || c.enqueue == nil {
			return ThreadReplyIgnored, nil
		}
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
func (c *ConversationCoordinator) RunSerialized(conversation ConversationID, run func()) {
	unlock := c.locks.lock(conversation)
	defer unlock()
	run()
}

// Lifecycle returns a stdin lifecycle bound to one routable conversation.
func (c *ConversationCoordinator) Lifecycle(conversation ConversationID) StdinLifecycle {
	if conversation.ChannelID == "" || conversation.RootTimestamp == "" {
		return nil
	}
	return conversationLifecycle{coordinator: c, conversation: conversation}
}

type conversationLifecycle struct {
	coordinator  *ConversationCoordinator
	conversation ConversationID
}

func (l conversationLifecycle) StdinReady(endpoint *InteractiveStdin) {
	l.coordinator.inputs.register(l.conversation, endpoint)
}

func (l conversationLifecycle) StdinClosed(endpoint *InteractiveStdin) {
	l.coordinator.inputs.unregister(l.conversation, endpoint)
}

type conversationInputs struct {
	mu     sync.Mutex
	inputs map[ConversationID]*InteractiveStdin
}

func (i *conversationInputs) register(conversation ConversationID, input *InteractiveStdin) {
	input.mu.Lock()
	defer input.mu.Unlock()
	if input.closed {
		return
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.inputs == nil {
		i.inputs = make(map[ConversationID]*InteractiveStdin)
	}
	i.inputs[conversation] = input
}

func (i *conversationInputs) lookup(conversation ConversationID) *InteractiveStdin {
	i.mu.Lock()
	defer i.mu.Unlock()
	return i.inputs[conversation]
}

func (i *conversationInputs) unregister(conversation ConversationID, input *InteractiveStdin) {
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.inputs[conversation] == input {
		delete(i.inputs, conversation)
	}
}

type conversationLocks struct {
	mu    sync.Mutex
	locks map[ConversationID]*sync.Mutex
}

func (l *conversationLocks) lock(conversation ConversationID) func() {
	if conversation.ChannelID == "" || conversation.RootTimestamp == "" {
		return func() {}
	}
	l.mu.Lock()
	if l.locks == nil {
		l.locks = make(map[ConversationID]*sync.Mutex)
	}
	lock := l.locks[conversation]
	if lock == nil {
		lock = &sync.Mutex{}
		l.locks[conversation] = lock
	}
	l.mu.Unlock()
	lock.Lock()
	return lock.Unlock
}

type conversationRoutes struct {
	mu       sync.Mutex
	capacity int
	entries  map[ConversationID]*list.Element
	lru      *list.List
}

type conversationRoute struct {
	key     ConversationID
	command *CommandConfig
}

func newConversationRoutes(capacity int) *conversationRoutes {
	return &conversationRoutes{capacity: capacity}
}

func (r *conversationRoutes) lookup(conversation ConversationID) (*CommandConfig, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.entries == nil {
		return nil, false
	}
	element := r.entries[conversation]
	if element == nil {
		return nil, false
	}
	r.lru.MoveToFront(element)
	return element.Value.(conversationRoute).command, true
}

func (r *conversationRoutes) store(conversation ConversationID, command *CommandConfig) {
	if r.capacity <= 0 {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.entries == nil {
		r.entries = make(map[ConversationID]*list.Element)
		r.lru = list.New()
	}
	if element := r.entries[conversation]; element != nil {
		element.Value = conversationRoute{key: conversation, command: command}
		r.lru.MoveToFront(element)
		return
	}
	element := r.lru.PushFront(conversationRoute{key: conversation, command: command})
	r.entries[conversation] = element
	if r.lru.Len() <= r.capacity {
		return
	}
	oldest := r.lru.Back()
	delete(r.entries, oldest.Value.(conversationRoute).key)
	r.lru.Remove(oldest)
}

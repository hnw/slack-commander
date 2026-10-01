package cmd

import (
	"container/list"
	"log"
	"sync"
)

// RootInputResolver はroute cache eviction後に起点senderの候補を解決する。
type RootInputResolver func(ConversationID) (RootCommandInput, error)

// RootCommandInput はroute復元時に起点textと候補indexを保持する。
type RootCommandInput struct {
	Text                  string
	AllowedCommandIndexes []int
}

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
	commands         *CommandSet
	resolveRootInput RootInputResolver
	enqueue          func(*CommandInput) bool
	routes           *conversationRoutes
	inputs           conversationInputs
	locks            conversationLocks
}

// NewConversationCoordinatorWithRootInputResolver はcache miss時に起点senderのACLでrouteを復元する。
func NewConversationCoordinatorWithRootInputResolver(commands *CommandSet, resolve RootInputResolver, enqueue func(*CommandInput) bool, routeCapacity int) *ConversationCoordinator {
	return &ConversationCoordinator{commands: commands, resolveRootInput: resolve, enqueue: enqueue, routes: newConversationRoutes(routeCapacity)}
}

// AcceptRoot queues a root command and records its route after queue acceptance.
func (c *ConversationCoordinator) AcceptRoot(input *CommandInput) bool {
	root := c.commands.MatchSingle(input.Text, input.AllowedCommandIndexes)
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
		if c.resolveRootInput == nil {
			return ThreadReplyIgnored, nil
		}
		var err error
		root, err = c.resolveRootCommand(input.ConversationID)
		if err != nil {
			return ThreadReplyIgnored, err
		}
		c.routes.store(input.ConversationID, root)
	}
	if root == nil {
		return ThreadReplyIgnored, nil
	}
	return c.routeThreadReply(root, input)
}

func (c *ConversationCoordinator) routeThreadReply(root *Command, input *CommandInput) (ThreadReplyResult, error) {
	switch root.config.ThreadReplyMode {
	case ThreadReplyIgnore:
		return ThreadReplyIgnored, nil
	case ThreadReplyStdin:
		if input.AllowedCommandIndexes != nil && !containsCommandIndex(input.AllowedCommandIndexes, root.config.Index) {
			return ThreadReplyIgnored, nil
		}
		endpoint := c.inputs.lookup(input.ConversationID)
		if endpoint == nil {
			return ThreadReplyIgnored, nil
		}
		if err := endpoint.TrySend(input.Text); err != nil {
			log.Printf("[WARN] dropping interactive stdin channel=%s thread=%s: %v", input.ConversationID.ChannelID, input.ConversationID.RootTimestamp, err)
		}
		return ThreadReplyRouted, nil
	case ThreadReplyCommand:
		if root.replies == nil || len(root.replies.commands) == 0 || c.enqueue == nil {
			return ThreadReplyIgnored, nil
		}
		input.CommandSet = root.replies
		if !c.enqueue(input) {
			return ThreadReplyQueueFull, nil
		}
		return ThreadReplyRouted, nil
	}
	return ThreadReplyIgnored, nil
}

func (c *ConversationCoordinator) resolveRootCommand(conversation ConversationID) (*Command, error) {
	if c.resolveRootInput == nil {
		return nil, nil
	}
	resolved, err := c.resolveRootInput(conversation)
	if err != nil {
		return nil, err
	}
	return c.commands.MatchSingle(resolved.Text, resolved.AllowedCommandIndexes), nil
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
	command *Command
}

func newConversationRoutes(capacity int) *conversationRoutes {
	return &conversationRoutes{capacity: capacity}
}

func (r *conversationRoutes) lookup(conversation ConversationID) (*Command, bool) {
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

func (r *conversationRoutes) store(conversation ConversationID, command *Command) {
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

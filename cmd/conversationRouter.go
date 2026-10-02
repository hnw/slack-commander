package cmd

import (
	"container/list"
	"sync"
)

// RootInputResolver はroute cache eviction後に起点senderの候補を解決する。
type RootInputResolver func(ConversationID) (RootCommandInput, error)

// RootCommandInput はroute復元時に起点textと候補indexを保持する。
type RootCommandInput struct {
	Text                  string
	AllowedCommandIndexes []int
}

// AcceptResult は入力受付後のrouting結果を表す。
type AcceptResult int

const (
	// AcceptIgnored は入力に対応する宛先がないことを表す。
	AcceptIgnored AcceptResult = iota
	// AcceptRouted は入力が宛先へ渡されたことを表す。
	AcceptRouted
	// AcceptQueueFull はqueueが満杯で入力を受け付けなかったことを表す。
	AcceptQueueFull
)

// ConversationRouter はroot commandとthread replyをroutingする。
type ConversationRouter struct {
	commands         *CommandSet
	resolveRootInput RootInputResolver
	dispatcher       *CommandDispatcher
	routes           *conversationRoutes
}

// NewConversationRouterWithRootInputResolver creates a router with a root input resolver.
func NewConversationRouterWithRootInputResolver(commands *CommandSet, resolve RootInputResolver, dispatcher *CommandDispatcher, routeCapacity int) *ConversationRouter {
	return &ConversationRouter{commands: commands, resolveRootInput: resolve, dispatcher: dispatcher, routes: newConversationRoutes(routeCapacity)}
}

// Accept はroot入力とthread replyをroutingする。
func (r *ConversationRouter) Accept(input *CommandInput) (AcceptResult, error) {
	if input.MessageID.Timestamp == input.ConversationID.RootTimestamp {
		return r.acceptRoot(input), nil
	}
	return r.acceptThreadReply(input)
}

func (r *ConversationRouter) acceptRoot(input *CommandInput) AcceptResult {
	root := r.commands.MatchSingle(input.Text, input.AllowedCommandIndexes)
	result := r.acceptDispatchResult(r.dispatcher.DispatchRoot(input))
	if result != AcceptRouted {
		return result
	}
	if root != nil {
		r.routes.store(input.ConversationID, root)
	}
	return AcceptRouted
}

func (r *ConversationRouter) acceptThreadReply(input *CommandInput) (AcceptResult, error) {
	root, found := r.routes.lookup(input.ConversationID)
	if !found {
		if r.resolveRootInput == nil {
			return AcceptIgnored, nil
		}
		var err error
		root, err = r.resolveRootCommand(input.ConversationID)
		if err != nil {
			return AcceptIgnored, err
		}
		r.routes.store(input.ConversationID, root)
	}
	if root == nil {
		return AcceptIgnored, nil
	}
	return r.routeThreadReply(root, input)
}

func (r *ConversationRouter) routeThreadReply(root *Command, input *CommandInput) (AcceptResult, error) {
	if root.replies == nil {
		return AcceptIgnored, nil
	}
	command, args := root.replies.MatchReply(input.Text, input.AllowedCommandIndexes)
	if command == nil {
		return AcceptIgnored, nil
	}
	return r.acceptDispatchResult(r.dispatcher.DispatchReply(command, args, root.replies, input)), nil
}

func (r *ConversationRouter) acceptDispatchResult(result DispatchResult) AcceptResult {
	switch result {
	case DispatchAccepted:
		return AcceptRouted
	case DispatchQueueFull:
		return AcceptQueueFull
	default:
		return AcceptIgnored
	}
}

func (r *ConversationRouter) resolveRootCommand(conversation ConversationID) (*Command, error) {
	if r.resolveRootInput == nil {
		return nil, nil
	}
	resolved, err := r.resolveRootInput(conversation)
	if err != nil {
		return nil, err
	}
	return r.commands.MatchSingle(resolved.Text, resolved.AllowedCommandIndexes), nil
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

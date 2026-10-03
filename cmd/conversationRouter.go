package cmd

import (
	"container/list"
	"slices"
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
	input.ResolvedInput = r.commands.ResolveInput(input.Text, input.AllowedCommandIndexes)
	result := r.acceptDispatchResult(r.dispatcher.Dispatch(input))
	if result != AcceptRouted {
		return result
	}
	if resolvedInputExecutable(input.ResolvedInput) {
		r.routes.store(input.ConversationID, matchedCommands(input.ResolvedInput))
	}
	return AcceptRouted
}

func (r *ConversationRouter) acceptThreadReply(input *CommandInput) (AcceptResult, error) {
	rootCommands, found := r.routes.lookup(input.ConversationID)
	if !found {
		if r.resolveRootInput == nil {
			return AcceptIgnored, nil
		}
		rootInput, err := r.resolveRootInput(input.ConversationID)
		if err != nil {
			return AcceptIgnored, err
		}
		root := r.commands.ResolveInput(rootInput.Text, rootInput.AllowedCommandIndexes)
		if !resolvedInputExecutable(root) {
			r.routes.store(input.ConversationID, nil)
			return AcceptIgnored, nil
		}
		rootCommands = matchedCommands(root)
		r.routes.store(input.ConversationID, rootCommands)
	}
	if rootCommands == nil {
		return AcceptIgnored, nil
	}
	return r.routeThreadReply(rootCommands, input)
}

func (r *ConversationRouter) routeThreadReply(root []*Command, input *CommandInput) (AcceptResult, error) {
	replies := replyCommands(root)
	if replies == nil {
		return AcceptIgnored, nil
	}
	input.ResolvedInput = replies.ResolveInput(input.Text, input.AllowedCommandIndexes)
	return r.acceptDispatchResult(r.dispatcher.Dispatch(input)), nil
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

type conversationRoutes struct {
	mu       sync.Mutex
	capacity int
	entries  map[ConversationID]*list.Element
	lru      *list.List
}

type conversationRoute struct {
	key      ConversationID
	commands []*Command
}

func newConversationRoutes(capacity int) *conversationRoutes {
	return &conversationRoutes{capacity: capacity}
}

func (r *conversationRoutes) lookup(conversation ConversationID) ([]*Command, bool) {
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
	return cloneCommands(element.Value.(conversationRoute).commands), true
}

func (r *conversationRoutes) store(conversation ConversationID, commands []*Command) {
	if r.capacity <= 0 {
		return
	}
	commands = cloneCommands(commands)
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.entries == nil {
		r.entries = make(map[ConversationID]*list.Element)
		r.lru = list.New()
	}
	if element := r.entries[conversation]; element != nil {
		element.Value = conversationRoute{key: conversation, commands: commands}
		r.lru.MoveToFront(element)
		return
	}
	element := r.lru.PushFront(conversationRoute{key: conversation, commands: commands})
	r.entries[conversation] = element
	if r.lru.Len() <= r.capacity {
		return
	}
	oldest := r.lru.Back()
	delete(r.entries, oldest.Value.(conversationRoute).key)
	r.lru.Remove(oldest)
}

func cloneCommands(commands []*Command) []*Command {
	return slices.Clone(commands)
}

func matchedCommands(parsed *ResolvedInput) []*Command {
	if parsed == nil {
		return nil
	}
	commands := make([]*Command, 0, len(parsed.Commands))
	for _, resolved := range parsed.Commands {
		if resolved.Command != nil {
			commands = append(commands, resolved.Command)
		}
	}
	return commands
}

func replyCommands(commands []*Command) *CommandSet {
	if len(commands) != 1 || commands[0] == nil {
		return nil
	}
	return commands[0].replies
}

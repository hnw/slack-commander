package cmd

import (
	"container/list"
	"context"
	"sync"
)

// RootInputResolver はroute cache eviction後に起点senderの候補を解決する。
type RootInputResolver func(context.Context, ConversationID) (RootCommandInput, error)

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
	stdinStore       *StdinStore
	resolveRootInput RootInputResolver
	dispatcher       *CommandDispatcher
	routes           *conversationRoutes
}

// NewConversationRouter creates a router with a root input resolver.
func NewConversationRouter(stdinStore *StdinStore, commands *CommandSet, resolve RootInputResolver, dispatcher *CommandDispatcher, routeCapacity int) *ConversationRouter {
	return &ConversationRouter{commands: commands, stdinStore: stdinStore, resolveRootInput: resolve, dispatcher: dispatcher, routes: newConversationRoutes(routeCapacity)}
}

// Accept はroot入力とthread replyをroutingする。
func (r *ConversationRouter) Accept(ctx context.Context, input *CommandInput) (AcceptResult, error) {
	if input.MessageID.Timestamp == input.ConversationID.RootTimestamp {
		return r.acceptRoot(input), nil
	}
	return r.acceptThreadReply(ctx, input)
}

func (r *ConversationRouter) acceptRoot(input *CommandInput) AcceptResult {
	input.ResolvedInput = r.commands.ResolveInput(input.Text, input.AllowedCommandIndexes)
	result := r.acceptDispatchResult(r.dispatcher.Dispatch(input))
	if result != AcceptRouted {
		return result
	}
	if resolvedInputExecutable(input.ResolvedInput) {
		r.routes.store(input.ConversationID, explicitReplyCommand(input.ResolvedInput))
	}
	return AcceptRouted
}

func (r *ConversationRouter) acceptThreadReply(ctx context.Context, input *CommandInput) (AcceptResult, error) {
	if entry, found := r.stdinStore.lookup(input.ConversationID); found {
		if entry.implicitReplyCommand == nil {
			return AcceptIgnored, nil
		}
		return r.routeThreadReply(NewCommandSet([]*Command{entry.implicitReplyCommand}), entry.endpoint, input)
	}
	explicitReply, found := r.routes.lookup(input.ConversationID)
	if !found {
		if r.resolveRootInput == nil {
			return AcceptIgnored, nil
		}
		rootInput, err := r.resolveRootInput(ctx, input.ConversationID)
		if err != nil {
			return AcceptIgnored, err
		}
		root := r.commands.ResolveInput(rootInput.Text, rootInput.AllowedCommandIndexes)
		if !resolvedInputExecutable(root) {
			r.routes.store(input.ConversationID, nil)
			return AcceptIgnored, nil
		}
		explicitReply = explicitReplyCommand(root)
		r.routes.store(input.ConversationID, explicitReply)
	}
	if explicitReply == nil {
		return AcceptIgnored, nil
	}
	return r.routeThreadReply(explicitReply.replies, nil, input)
}

func (r *ConversationRouter) routeThreadReply(replyCommands *CommandSet, stdinTarget *InteractiveStdin, input *CommandInput) (AcceptResult, error) {
	input.ResolvedInput = replyCommands.ResolveInput(input.Text, input.AllowedCommandIndexes)
	if stdinTarget != nil && resolvedInputExecutable(input.ResolvedInput) {
		input.stdinTarget = stdinTarget
	}
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
	key                  ConversationID
	explicitReplyCommand *Command
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
	return element.Value.(conversationRoute).explicitReplyCommand, true
}

func (r *conversationRoutes) store(conversation ConversationID, explicitReplyCommand *Command) {
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
		element.Value = conversationRoute{key: conversation, explicitReplyCommand: explicitReplyCommand}
		r.lru.MoveToFront(element)
		return
	}
	element := r.lru.PushFront(conversationRoute{key: conversation, explicitReplyCommand: explicitReplyCommand})
	r.entries[conversation] = element
	if r.lru.Len() <= r.capacity {
		return
	}
	oldest := r.lru.Back()
	delete(r.entries, oldest.Value.(conversationRoute).key)
	r.lru.Remove(oldest)
}

func explicitReplyCommand(parsed *ResolvedInput) *Command {
	if parsed == nil || len(parsed.Commands) != 1 {
		return nil
	}
	command := parsed.Commands[0].Command
	if command == nil || command.config.InteractiveStdin || command.replies == nil {
		return nil
	}
	return command
}

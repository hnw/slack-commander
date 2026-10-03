package cmd

import "sync"

// StdinLifecycle はstdin endpointの利用開始と終了をStoreへ通知する。
type StdinLifecycle interface {
	StdinReady(*InteractiveStdin, *Command)
	StdinClosed(*InteractiveStdin)
}

// StdinStore はconversationごとのactive stdin endpointを保持する。
type StdinStore struct {
	mu     sync.Mutex
	inputs map[ConversationID]stdinEntry
}

type stdinEntry struct {
	endpoint             *InteractiveStdin
	implicitReplyCommand *Command
}

// Lifecycle はconversationに紐づくendpoint lifecycleを返す。
func (s *StdinStore) Lifecycle(conversation ConversationID) StdinLifecycle {
	if conversation.ChannelID == "" || conversation.RootTimestamp == "" {
		return nil
	}
	return stdinLifecycle{store: s, conversation: conversation}
}

type stdinLifecycle struct {
	store        *StdinStore
	conversation ConversationID
}

func (l stdinLifecycle) StdinReady(endpoint *InteractiveStdin, implicitReplyCommand *Command) {
	l.store.register(l.conversation, endpoint, implicitReplyCommand)
}

func (l stdinLifecycle) StdinClosed(endpoint *InteractiveStdin) {
	l.store.unregister(l.conversation, endpoint)
}

func (s *StdinStore) register(conversation ConversationID, endpoint *InteractiveStdin, implicitReplyCommand *Command) {
	endpoint.mu.Lock()
	defer endpoint.mu.Unlock()
	if endpoint.closed {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.inputs == nil {
		s.inputs = make(map[ConversationID]stdinEntry)
	}
	s.inputs[conversation] = stdinEntry{endpoint: endpoint, implicitReplyCommand: implicitReplyCommand}
}

func (s *StdinStore) lookup(conversation ConversationID) (stdinEntry, bool) {
	if s == nil {
		return stdinEntry{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.inputs[conversation]
	return entry, ok
}

func (s *StdinStore) unregister(conversation ConversationID, endpoint *InteractiveStdin) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if entry, ok := s.inputs[conversation]; ok && entry.endpoint == endpoint {
		delete(s.inputs, conversation)
	}
}

package cmd

import "sync"

// ThreadLocks serializes command execution within one Slack thread.
type ThreadLocks struct {
	mu    sync.Mutex
	locks map[ThreadKey]*sync.Mutex
}

// Lock returns an unlock function for the given Slack conversation.
func (l *ThreadLocks) Lock(context ConversationContext) func() {
	if l == nil || context.ChannelID == "" || context.RootThreadTimestamp == "" {
		return func() {}
	}
	//nolint:staticcheck // ThreadKey remains limited to Slack thread identifiers.
	key := ThreadKey{
		ChannelID:           context.ChannelID,
		RootThreadTimestamp: context.RootThreadTimestamp,
	}
	l.mu.Lock()
	if l.locks == nil {
		l.locks = make(map[ThreadKey]*sync.Mutex)
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

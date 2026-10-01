package cmd

import "sync"

// ConversationLocks はconversationごとのcommand実行を直列化する。
type ConversationLocks struct {
	mu    sync.Mutex
	locks map[ConversationID]*sync.Mutex
}

// Lock はconversation lockを取得し、解放関数を返す。
func (l *ConversationLocks) Lock(conversation ConversationID) func() {
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

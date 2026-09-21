package cmd

import "sync"

// ThreadKey は入力イベントの付随情報に依存せず送信先を特定する。
type ThreadKey struct {
	ChannelID           string
	RootThreadTimestamp string
}

// ThreadInputRegistry は worker をまたいで現在の送信先だけを共有する。
type ThreadInputRegistry struct {
	mu     sync.Mutex
	inputs map[ThreadKey]*InteractiveStdin
}

// Register は多重起動を制御せず、最後に登録された process を送信先にする。
func (r *ThreadInputRegistry) Register(key ThreadKey, input *InteractiveStdin) {
	input.mu.Lock()
	defer input.mu.Unlock()
	if input.closed {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.inputs == nil {
		r.inputs = make(map[ThreadKey]*InteractiveStdin)
	}
	r.inputs[key] = input
}

// Lookup は registry を持たない既存の呼び出し経路でも通常 routing を維持する。
func (r *ThreadInputRegistry) Lookup(key ThreadKey) *InteractiveStdin {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.inputs[key]
}

// Unregister は古い process の終了で新しい登録を消さないようにする。
func (r *ThreadInputRegistry) Unregister(key ThreadKey, input *InteractiveStdin) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.inputs[key] == input {
		delete(r.inputs, key)
	}
}

package cmd

import "sync"

type ThreadKey struct {
	ChannelID           string
	RootThreadTimestamp string
}

type LiveInputRegistry struct {
	mu     sync.Mutex
	inputs map[ThreadKey]*LiveInput
}

func (r *LiveInputRegistry) Register(key ThreadKey, input *LiveInput) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.inputs == nil {
		r.inputs = make(map[ThreadKey]*LiveInput)
	}
	r.inputs[key] = input
}

func (r *LiveInputRegistry) Lookup(key ThreadKey) *LiveInput {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.inputs[key]
}

func (r *LiveInputRegistry) Unregister(key ThreadKey, input *LiveInput) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.inputs[key] == input {
		delete(r.inputs, key)
	}
}

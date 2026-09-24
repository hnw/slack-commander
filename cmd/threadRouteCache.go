package cmd

import (
	"container/list"
	"sync"
)

// ThreadRouteCache caches the root command that owns a thread reply route.
// A nil command is a negative result; lookup failures are intentionally not stored.
type ThreadRouteCache struct {
	mu       sync.Mutex
	capacity int
	entries  map[ThreadKey]*list.Element
	lru      *list.List
}

type threadRouteEntry struct {
	key     ThreadKey
	command *CommandConfig
}

// NewThreadRouteCache creates a bounded cache. Non-positive capacities disable storage.
func NewThreadRouteCache(capacity int) *ThreadRouteCache {
	return &ThreadRouteCache{capacity: capacity}
}

// Lookup returns a positive or negative cached route.
func (c *ThreadRouteCache) Lookup(key ThreadKey) (*CommandConfig, bool) {
	if c == nil {
		return nil, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries == nil {
		return nil, false
	}
	element := c.entries[key]
	if element == nil {
		return nil, false
	}
	c.lru.MoveToFront(element)
	return element.Value.(threadRouteEntry).command, true
}

// Store records either a positive command or a negative nil result.
func (c *ThreadRouteCache) Store(key ThreadKey, command *CommandConfig) {
	if c == nil || c.capacity <= 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries == nil {
		c.entries = make(map[ThreadKey]*list.Element)
		c.lru = list.New()
	}
	if element := c.entries[key]; element != nil {
		element.Value = threadRouteEntry{key: key, command: command}
		c.lru.MoveToFront(element)
		return
	}
	element := c.lru.PushFront(threadRouteEntry{key: key, command: command})
	c.entries[key] = element
	if c.lru.Len() <= c.capacity {
		return
	}
	oldest := c.lru.Back()
	delete(c.entries, oldest.Value.(threadRouteEntry).key)
	c.lru.Remove(oldest)
}

// Clear discards every cached resolution, for example after a future config reload.
func (c *ThreadRouteCache) Clear() {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries = nil
	c.lru = nil
}

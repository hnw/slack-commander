package cmd

import "testing"

func TestThreadRouteCacheKeepsPositiveAndNegativeRoutes(t *testing.T) {
	cache := NewThreadRouteCache(2)
	positive := NewCommandConfig(&Definition{Keyword: "todo"}, nil)
	key := ThreadKey{ChannelID: "C", RootThreadTimestamp: "1"}
	negative := ThreadKey{ChannelID: "C", RootThreadTimestamp: "2"}
	cache.Store(key, positive)
	cache.Store(negative, nil)
	if got, ok := cache.Lookup(key); !ok || got != positive {
		t.Fatalf("positive route = %v, %v", got, ok)
	}
	if got, ok := cache.Lookup(negative); !ok || got != nil {
		t.Fatalf("negative route = %v, %v", got, ok)
	}
}

func TestThreadRouteCacheEvictsLeastRecentlyUsedRoute(t *testing.T) {
	cache := NewThreadRouteCache(2)
	first := ThreadKey{ChannelID: "C", RootThreadTimestamp: "1"}
	second := ThreadKey{ChannelID: "C", RootThreadTimestamp: "2"}
	third := ThreadKey{ChannelID: "C", RootThreadTimestamp: "3"}
	cache.Store(first, nil)
	cache.Store(second, nil)
	_, _ = cache.Lookup(first)
	cache.Store(third, nil)
	if _, ok := cache.Lookup(second); ok {
		t.Fatal("least recently used route survived eviction")
	}
	if _, ok := cache.Lookup(first); !ok {
		t.Fatal("recent route was evicted")
	}
}

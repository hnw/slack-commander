package cmd

import (
	"sync"
	"testing"
	"time"
)

func TestConversationLocksSerializesSameConversation(t *testing.T) {
	locks := &ConversationLocks{}
	conversation := ConversationID{ChannelID: "C", RootTimestamp: "1"}
	started := make(chan struct{}, 2)
	release := make(chan struct{}, 2)
	defer close(release)
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			unlock := locks.Lock(conversation)
			defer unlock()
			started <- struct{}{}
			<-release
		}()
	}
	<-started
	select {
	case <-started:
		t.Fatal("same conversation ran concurrently")
	case <-time.After(50 * time.Millisecond):
	}
	release <- struct{}{}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("second command did not start")
	}
	release <- struct{}{}
	wg.Wait()
}

func TestConversationLocksRunsDifferentConversationsConcurrently(t *testing.T) {
	locks := &ConversationLocks{}
	started := make(chan struct{}, 2)
	release := make(chan struct{}, 2)
	defer close(release)
	var wg sync.WaitGroup
	for _, root := range []string{"1", "2"} {
		wg.Add(1)
		go func(root string) {
			defer wg.Done()
			unlock := locks.Lock(ConversationID{ChannelID: "C", RootTimestamp: root})
			defer unlock()
			started <- struct{}{}
			<-release
		}(root)
	}
	for range 2 {
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("different conversations did not run concurrently")
		}
	}
	release <- struct{}{}
	release <- struct{}{}
	wg.Wait()
}

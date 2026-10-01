package cmd

import (
	"io"
	"testing"
)

func TestStdinStoreKeepsNewestEndpoint(t *testing.T) {
	store := &StdinStore{}
	conversation := ConversationID{ChannelID: "C", RootTimestamp: "1"}
	_, oldWriter := io.Pipe()
	_, newWriter := io.Pipe()
	old := NewInteractiveStdin(oldWriter, "", func(error) {})
	newEndpoint := NewInteractiveStdin(newWriter, "", func(error) {})
	lifecycle := store.Lifecycle(conversation)
	lifecycle.StdinReady(old)
	lifecycle.StdinReady(newEndpoint)
	lifecycle.StdinClosed(old)
	if store.lookup(conversation) != newEndpoint {
		t.Fatal("old endpoint close removed the new endpoint")
	}
	lifecycle.StdinClosed(newEndpoint)
	if store.lookup(conversation) != nil {
		t.Fatal("closed endpoint remained registered")
	}
	old.Close()
	newEndpoint.Close()
	_ = oldWriter.Close()
	_ = newWriter.Close()
}

func TestStdinStoreRejectsInvalidAndClosedEndpoints(t *testing.T) {
	store := &StdinStore{}
	if store.Lifecycle(ConversationID{ChannelID: "C"}) != nil {
		t.Fatal("invalid conversation got a lifecycle")
	}
	conversation := ConversationID{ChannelID: "C", RootTimestamp: "1"}
	_, writer := io.Pipe()
	endpoint := NewInteractiveStdin(writer, "", func(error) {})
	endpoint.Close()
	store.Lifecycle(conversation).StdinReady(endpoint)
	if store.lookup(conversation) != nil {
		t.Fatal("closed endpoint was registered")
	}
	_ = writer.Close()
}

package cmd

import (
	"bufio"
	"errors"
	"io"
	"testing"
	"time"
)

func TestLiveInputOrderAndBackpressure(t *testing.T) {
	r, w := io.Pipe()
	defer func() { _ = r.Close() }()
	e := NewLiveInput(w, "initial", func(err error) { t.Error(err) })
	defer e.Close()
	if err := e.TrySend("<@U> “a” <https://example.com|link>"); err != nil {
		t.Fatal(err)
	}
	if err := e.TrySend("dropped"); !errors.Is(err, ErrLiveInputBusy) {
		t.Fatalf("expected busy, got %v", err)
	}
	reader := bufio.NewReader(r)
	for _, want := range []string{"initial\n", "<@U> “a” <https://example.com|link>\n"} {
		got, err := reader.ReadString('\n')
		if err != nil || got != want {
			t.Fatalf("got=%q err=%v want=%q", got, err, want)
		}
	}
}

func TestLiveInputEmptyInitialAndClose(t *testing.T) {
	r, w := io.Pipe()
	defer func() { _ = r.Close() }()
	e := NewLiveInput(w, "", func(err error) { t.Error(err) })
	if err := e.TrySend("alpha\n"); err != nil {
		t.Fatal(err)
	}
	got, err := bufio.NewReader(r).ReadString('\n')
	if err != nil || got != "alpha\n" {
		t.Fatalf("got=%q err=%v", got, err)
	}
	if err := e.TrySend("blocked"); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { e.Close(); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("close did not unblock writer")
	}
	if err := e.TrySend("late"); !errors.Is(err, ErrLiveInputClosed) {
		t.Fatalf("expected closed, got %v", err)
	}
}

func TestLiveInputReportsWriteError(t *testing.T) {
	r, w := io.Pipe()
	_ = r.Close()
	errs := make(chan error, 1)
	e := NewLiveInput(w, "initial", func(err error) { errs <- err })
	defer e.Close()
	select {
	case err := <-errs:
		if !errors.Is(err, io.ErrClosedPipe) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("no write error")
	}
	<-e.finished
	if err := e.TrySend("late"); !errors.Is(err, ErrLiveInputClosed) {
		t.Fatal(err)
	}
}

func TestLiveInputRegistryReplacement(t *testing.T) {
	var registry LiveInputRegistry
	key := ThreadKey{ChannelID: "C", RootThreadTimestamp: "1"}
	a, b := &LiveInput{}, &LiveInput{}
	registry.Register(key, a)
	if registry.Lookup(key) != a {
		t.Fatal("registration not found")
	}
	registry.Register(key, b)
	registry.Unregister(key, a)
	if registry.Lookup(key) != b {
		t.Fatal("old process removed new registration")
	}
	for _, other := range []ThreadKey{
		{ChannelID: "C", RootThreadTimestamp: "other"},
		{ChannelID: "other", RootThreadTimestamp: "1"},
	} {
		if registry.Lookup(other) != nil {
			t.Fatalf("cross-thread route for %+v", other)
		}
	}
	registry.Unregister(key, b)
	if registry.Lookup(key) != nil {
		t.Fatal("old endpoint restored")
	}
}

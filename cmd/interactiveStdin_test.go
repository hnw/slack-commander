package cmd

import (
	"bufio"
	"errors"
	"io"
	"testing"
	"testing/synctest"
	"time"
)

func TestInteractiveStdinOrderAndBackpressure(t *testing.T) {
	r, w := io.Pipe()
	defer func() { _ = r.Close() }()
	e := NewInteractiveStdin(w, "initial", func(err error) { t.Error(err) })
	defer e.Close()
	if err := e.TrySend("<@U> “a” <https://example.com|link>"); err != nil {
		t.Fatal(err)
	}
	if err := e.TrySend("dropped"); !errors.Is(err, ErrInteractiveStdinBusy) {
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

func TestInteractiveStdinEmptyInitialAndClose(t *testing.T) {
	r, w := io.Pipe()
	defer func() { _ = r.Close() }()
	e := NewInteractiveStdin(w, "", func(err error) { t.Error(err) })
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
	if err := e.TrySend("late"); !errors.Is(err, ErrInteractiveStdinClosed) {
		t.Fatalf("expected closed, got %v", err)
	}
}

func TestInteractiveStdinReportsWriteError(t *testing.T) {
	r, w := io.Pipe()
	_ = r.Close()
	errs := make(chan error, 1)
	e := NewInteractiveStdin(w, "initial", func(err error) { errs <- err })
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
	if err := e.TrySend("late"); !errors.Is(err, ErrInteractiveStdinClosed) {
		t.Fatal(err)
	}
}

func TestThreadInputRegistryReplacement(t *testing.T) {
	var registry ThreadInputRegistry
	key := ThreadKey{ChannelID: "C", RootThreadTimestamp: "1"}
	a := newInteractiveStdinSession("", 0, nil)
	b := newInteractiveStdinSession("", 0, nil)
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

func TestThreadInputRegistryIdleCleanupKeepsNewRegistration(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var registry ThreadInputRegistry
		key := ThreadKey{ChannelID: "C", RootThreadTimestamp: "1"}
		r, w := io.Pipe()
		defer func() { _ = r.Close() }()
		old := newInteractiveStdinSession("", time.Second, nil)
		old.onClose = func() { registry.Unregister(key, old) }
		defer old.Close()
		old.Start(w)
		registry.Register(key, old)
		newer := newInteractiveStdinSession("", 0, nil)
		defer newer.Close()
		registry.Register(key, newer)
		time.Sleep(time.Second)
		synctest.Wait()
		registry.Register(key, old)
		if registry.Lookup(key) != newer {
			t.Fatal("closed endpoint replaced or removed newer registration")
		}
	})
}

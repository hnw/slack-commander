package cmd

import (
	"errors"
	"io"
	"testing"
	"testing/synctest"
	"time"
)

func TestStdinSessionFinitePreservesInput(t *testing.T) {
	for _, initial := range []string{"", "no-final-newline", "two\nlines\n"} {
		r, w := io.Pipe()
		s := newStdinSession(initial, nil)
		s.Start(w)
		got, err := io.ReadAll(r)
		s.Close()
		_ = r.Close()
		if err != nil || string(got) != initial {
			t.Fatalf("input=%q got=%q err=%v", initial, got, err)
		}
	}
}

func TestStdinSessionIdleEOF(t *testing.T) {
	for _, initial := range []string{"", "initial"} {
		t.Run(initial, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				r, w := io.Pipe()
				defer func() { _ = r.Close() }()
				e := newInteractiveStdinSession(initial, time.Second, nil)
				defer e.Close()
				e.Start(w)
				var got []byte
				var err error
				go func() { got, err = io.ReadAll(r) }()
				time.Sleep(time.Second)
				synctest.Wait()
				want := initial
				if want != "" {
					want += "\n"
				}
				if string(got) != want || err != nil {
					t.Fatalf("got=%q err=%v", got, err)
				}
				if err := e.TrySend("late"); !errors.Is(err, ErrLiveInputClosed) {
					t.Fatal(err)
				}
				e.Close()
			})
		})
	}
}

func TestStdinSessionAcceptedInputExtendsIdle(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r, w := io.Pipe()
		defer func() { _ = r.Close() }()
		e := newInteractiveStdinSession("", time.Second, nil)
		defer e.Close()
		e.Start(w)
		go func() { _, _ = io.Copy(io.Discard, r) }()
		for range 3 {
			time.Sleep(750 * time.Millisecond)
			if err := e.TrySend("reply"); err != nil {
				t.Fatal(err)
			}
			synctest.Wait()
		}
		time.Sleep(time.Second)
		synctest.Wait()
		if err := e.TrySend("late"); !errors.Is(err, ErrLiveInputClosed) {
			t.Fatal(err)
		}
	})
}

func TestStdinSessionBusyDoesNotExtendIdle(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r, w := io.Pipe()
		defer func() { _ = r.Close() }()
		e := newInteractiveStdinSession("blocked initial", time.Second, nil)
		defer e.Close()
		e.Start(w)
		time.Sleep(500 * time.Millisecond)
		if err := e.TrySend("pending"); err != nil {
			t.Fatal(err)
		}
		time.Sleep(750 * time.Millisecond)
		if err := e.TrySend("dropped"); !errors.Is(err, ErrLiveInputBusy) {
			t.Fatal(err)
		}
		time.Sleep(250 * time.Millisecond)
		synctest.Wait()
		if err := e.TrySend("late"); !errors.Is(err, ErrLiveInputClosed) {
			t.Fatal(err)
		}
	})
}

func TestStdinSessionCloseRaces(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		for range 30 {
			r, w := io.Pipe()
			e := newInteractiveStdinSession("blocked", time.Second, nil)
			e.Start(w)
			go func() { time.Sleep(time.Second); e.Close() }()
			go func() { time.Sleep(time.Second); _ = e.TrySend("racing") }()
			time.Sleep(time.Second)
			synctest.Wait()
			e.Close()
			_ = r.Close()
			if err := e.TrySend("late"); !errors.Is(err, ErrLiveInputClosed) {
				t.Fatal(err)
			}
		}
	})
}

func TestStdinSessionIdleDisabled(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r, w := io.Pipe()
		defer func() { _ = r.Close() }()
		e := newInteractiveStdinSession("", 0, nil)
		defer e.Close()
		e.Start(w)
		time.Sleep(24 * time.Hour)
		if err := e.TrySend("still open"); err != nil {
			t.Fatal(err)
		}
	})
}

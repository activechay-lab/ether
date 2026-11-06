package generationfence

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"
)

// Check cost follows requests/elapsed time, never body size. Simulated monotonic
// time also proves the exact lost-notification bound without sleeping.
func TestWriterAuthorityBudgetAndFencing(t *testing.T) {
	for _, writes := range []int{1, 4096} {
		t.Run(strconv.Itoa(writes), func(t *testing.T) {
			now := time.Now()
			start := now
			checks, local := 0, 0
			stale, sealed := false, false
			var notify func()
			closed := false
			out := httptest.NewRecorder()
			w := &Writer{ResponseWriter: out, now: func() time.Time { return now }, Check: func() error {
				checks++
				if stale {
					return ErrStale
				}
				return nil
			}, LocalCheck: func() error {
				local++
				if sealed {
					return ErrStale
				}
				return nil
			}, Watch: func(fn func()) func() { notify = fn; return func() { closed = true } }}
			w.WriteHeader(http.StatusOK)
			for i := 0; i < writes; i++ {
				if _, err := w.Write([]byte("media")); err != nil {
					t.Fatal(err)
				}
			}
			w.Flush()
			if checks != 1 || local < writes {
				t.Fatalf("checks=%d local=%d writes=%d", checks, local, writes)
			}
			stale = true
			now = start.Add(MaxStaleness - time.Nanosecond)
			if _, err := w.Write([]byte("bounded")); err != nil {
				t.Fatal(err)
			}
			now = start.Add(MaxStaleness)
			if _, err := w.Write([]byte("stale")); !errors.Is(err, ErrStale) {
				t.Fatal("writer exceeded staleness bound", err)
			}
			if checks != 2 {
				t.Fatal(checks)
			}
			stale = false
			notify()
			if _, err := w.Write(nil); !errors.Is(err, ErrStale) {
				t.Fatal("denial was revived")
			}
			w.Close()
			if !closed {
				t.Fatal("subscription leaked")
			}
		})
	}
	for _, reason := range []string{"notification", "local seal", "slow check"} {
		t.Run(reason, func(t *testing.T) {
			now := time.Now()
			stale := false
			var notify func()
			out := httptest.NewRecorder()
			w := &Writer{ResponseWriter: out, now: func() time.Time { return now }, Check: func() error {
				if stale {
					if reason == "slow check" {
						now = now.Add(MaxStaleness)
						return nil
					}
					return ErrStale
				}
				return nil
			}, LocalCheck: func() error {
				if stale && reason == "local seal" {
					return ErrStale
				}
				return nil
			}, Watch: func(fn func()) func() { notify = fn; return func() {} }}
			defer w.Close()
			w.WriteHeader(200)
			stale = true
			if reason == "notification" {
				notify()
			}
			if reason == "slow check" {
				now = now.Add(MaxStaleness)
			}
			if _, err := w.Write([]byte("stale")); !errors.Is(err, ErrStale) {
				t.Fatal("stale bytes published", err)
			}
			if out.Body.Len() != 0 {
				t.Fatal(out.Body.String())
			}
		})
	}
}

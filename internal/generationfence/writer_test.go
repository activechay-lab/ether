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
	for _, reason := range []string{"notification", "slow check", "repeated notifications", "repeated slow checks", "negative", "local seal"} {
		t.Run(reason, func(t *testing.T) {
			now := time.Now()
			calls := 0
			var notify func()
			out := httptest.NewRecorder()
			w := &Writer{ResponseWriter: out, now: func() time.Time { return now }, Watch: func(fn func()) func() { notify = fn; return func() {} }, Check: func() error {
				calls++
				if out.Body.Len() != 0 {
					t.Fatal("published before fresh observation")
				}
				switch reason {
				case "notification":
					if calls == 1 {
						notify()
					}
				case "slow check":
					if calls == 1 {
						now = now.Add(MaxStaleness)
					}
				case "repeated notifications":
					notify()
				case "repeated slow checks":
					now = now.Add(MaxStaleness)
				case "negative":
					return ErrStale
				}
				return nil
			}, LocalCheck: func() error {
				if reason == "local seal" {
					return ErrStale
				}
				return nil
			}}
			defer w.Close()
			_, err := w.Write([]byte("media"))
			if reason == "notification" || reason == "slow check" {
				if err != nil || calls != 2 || out.Body.String() != "media" || out.Code != http.StatusOK {
					t.Fatalf("healthy writer interrupted: checks=%d status=%d body=%q err=%v", calls, out.Code, out.Body.String(), err)
				}
			} else {
				expected := 3
				if reason == "negative" {
					expected = 1
				}
				if reason == "local seal" {
					expected = 0
				}
				if !errors.Is(err, ErrStale) || calls != expected || out.Code != http.StatusConflict {
					t.Fatalf("checks=%d status=%d err=%v", calls, out.Code, err)
				}
			}
		})
	}
}

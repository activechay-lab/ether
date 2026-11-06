package nodeinput

import (
	"bytes"
	"context"
	"io"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/nodesource"
)

// A real loopback HTTP read across two independent private mTLS transports.
// Cancelled source producers deliberately try to return late corrupt bytes.
func TestSourceFailoverByteContinuity(t *testing.T) {
	for _, stall := range []bool{false, true} {
		name := "partial-body"
		if stall {
			name = "stalled-body"
		}
		t.Run(name, func(t *testing.T) {
			a, b := setup(t), setup(t)
			next := b.grant
			next.Scope.SourceGeneration = 1
			next.Credential = "replacement"
			next.Expires = a.grant.Expires
			b.a.mu.Lock()
			b.a.grants[next.Credential] = next
			b.a.mu.Unlock()
			late := make(chan struct{})
			a.src.read = func(ctx context.Context, p []byte, off int64) (int, error) {
				if stall {
					<-ctx.Done()
					close(late)
					return copy(p, bytes.Repeat([]byte("X"), len(p))), nil
				}
				return copy(p, []byte("01234")), io.ErrUnexpectedEOF
			}
			a.g.o.ReadTimeout = 50 * time.Millisecond
			a.g.o.MaxRecoveries = 2
			calls := 0
			a.g.o.Recover = func(ctx context.Context, old nodesource.Grant) (*nodesource.Client, nodesource.Grant, error) {
				calls++
				if old != a.grant {
					t.Error("wrong previous source")
				}
				// Model the controller's committed generation CAS fencing source A.
				a.a.mu.Lock()
				delete(a.a.grants, old.Credential)
				a.a.mu.Unlock()
				return b.g.o.Client, next, nil
			}
			if e := a.in.ClaimInput(func(error) {}); e != nil {
				t.Fatal(e)
			}
			before := time.Now()
			code, body, _ := fetch(t, a.in, "GET", "")
			if code != 200 || body != "0123456789abcdef" || calls != 1 {
				t.Fatalf("code=%d bytes=%q recoveries=%d", code, body, calls)
			}
			if stall {
				select {
				case <-late:
				case <-time.After(time.Second):
					t.Fatal("old producer still running")
				}
			}
			if _, e := a.g.o.Client.Open(context.Background(), a.grant); e == nil {
				t.Fatal("old source grant accepted after movement")
			}
			t.Logf("byte-identical HTTP body; recoveries=%d interruption=%s old grant rejected", calls, time.Since(before))
		})
	}
}

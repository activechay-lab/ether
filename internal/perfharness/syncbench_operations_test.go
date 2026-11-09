package perfharness

import (
	"context"
	"errors"
	"testing"
)

// The former one-percent allowance accepted this single rejected operation.
// Successful operations must remain visible without masking the rejection.
func TestSyncBenchRejectsAnyDetailFailure(t *testing.T) {
	rejected := errors.New("injected write rejection")
	for _, fail := range []bool{false, true} {
		counts, err := runSyncBenchOperations(context.Background(), 100, func(_ context.Context, i int) error {
			if fail && i == 50 {
				return rejected
			}
			return nil
		})
		want := SyncBenchOperations{Attempted: 100, Succeeded: 100}
		if fail {
			want.Succeeded, want.Rejected = 99, 1
		}
		if *counts != want || errors.Is(err, rejected) != fail || (err == nil) == fail {
			t.Fatalf("injected=%v counts=%+v want=%+v err=%v", fail, counts, want, err)
		}
	}
}

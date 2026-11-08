package store

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

// checkpointBeginProbe observes the actual driver BEGIN boundary, after
// stmtConn has acquired the Go gate. It changes no production behavior.
type checkpointBeginProbe struct {
	driver.Conn
	before func()
}

func (p *checkpointBeginProbe) BeginTx(ctx context.Context, opts driver.TxOptions) (driver.Tx, error) {
	p.before()
	return p.Conn.(driver.ConnBeginTx).BeginTx(ctx, opts)
}

// TestCheckpointWriterGateMechanism distinguishes underlying SQLite BEGIN
// contention from Go-gate admission. It establishes a small pinned-reader
// control and reports INCONCLUSIVE if a TRUNCATE overlap is not observed.
// It does not reproduce or attribute any historical benchmark failure.
func TestCheckpointWriterGateMechanism(t *testing.T) {
	// This is intentionally a recognizer for statements we emit, not a SQL
	// parser. All other PRAGMAs retain their existing admission behavior.
	for q, want := range map[string]bool{
		"PRAGMA wal_checkpoint(TRUNCATE)":       true,
		" \tpragma\nwal_checkpoint(truncate); ": true,
		"PRAGMA wal_checkpoint(PASSIVE)":        false,
		"PRAGMA busy_timeout=250":               false,
		"PRAGMA journal_mode":                   false,
	} {
		if got := writes(q); got != want {
			t.Fatalf("writes(%q)=%v, want %v", q, got, want)
		}
	}
	for _, tc := range []struct {
		name, mode                        string
		ungated, releaseEarly, queryError bool
	}{
		{name: "PASSIVE", mode: "PASSIVE"},
		{name: "TRUNCATE_ungated_control", mode: "TRUNCATE", ungated: true},
		{name: "TRUNCATE_busy", mode: "TRUNCATE"},
		{name: "TRUNCATE_reader_release", mode: "TRUNCATE", releaseEarly: true},
		{name: "TRUNCATE_query_error", mode: "TRUNCATE", queryError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mode := tc.mode
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			db, err := OpenPath(ctx, filepath.Join(t.TempDir(), "mechanism.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			db.SetMaxOpenConns(3)
			if _, err := db.ExecContext(ctx, `CREATE TABLE mechanism(id INTEGER PRIMARY KEY, v TEXT); INSERT INTO mechanism VALUES(1,'old')`); err != nil {
				t.Fatal(err)
			}
			if err := Checkpoint(ctx, db, true); err != nil {
				t.Fatal(err)
			}
			conn := func() *sql.Conn {
				c, err := db.Conn(ctx)
				if err != nil {
					t.Fatal(err)
				}
				return c
			}
			reader, checkpoint, writer := conn(), conn(), conn()
			defer reader.Close()
			defer checkpoint.Close()
			defer writer.Close()
			if _, err := reader.ExecContext(ctx, `BEGIN`); err != nil {
				t.Fatal(err)
			}
			pinned := true
			releaseReader := func() {
				if pinned {
					_, _ = reader.ExecContext(context.Background(), `ROLLBACK`)
					pinned = false
				}
			}
			defer releaseReader()
			var value string
			if err := reader.QueryRowContext(ctx, `SELECT v FROM mechanism WHERE id=1`).Scan(&value); err != nil || value != "old" {
				t.Fatalf("pin reader: value=%q err=%v", value, err)
			}
			if _, err := writer.ExecContext(ctx, `UPDATE mechanism SET v='new' WHERE id=1`); err != nil {
				t.Fatal(err)
			}
			// Test-local connection settings only. No global or production limits.
			if _, err := checkpoint.ExecContext(ctx, `PRAGMA busy_timeout=250`); err != nil {
				t.Fatal(err)
			}
			if _, err := writer.ExecContext(ctx, `PRAGMA busy_timeout=20`); err != nil {
				t.Fatal(err)
			}
			var gate *writeGate
			if err := writer.Raw(func(raw any) error { gate = raw.(*stmtConn).gate; return nil }); err != nil {
				t.Fatal(err)
			}
			if tc.ungated {
				// Explicit test-only bypass, retaining the original mechanism
				// control without a production switch or old implementation.
				if err := checkpoint.Raw(func(raw any) error { raw.(*stmtConn).gate = nil; return nil }); err != nil {
					t.Fatal(err)
				}
				defer checkpoint.Raw(func(raw any) error { raw.(*stmtConn).gate = gate; return nil })
			}
			entered, done := make(chan struct{}), make(chan struct{})
			var cpStart, cpEnd time.Time
			var busy, frames, copied int
			var cpErr error
			go func() {
				defer close(done)
				cpStart = time.Now()
				close(entered) // checkpoint dispatch barrier, not lock-acquired proof
				if tc.queryError {
					// A live read transaction makes checkpointing on that same
					// connection fail in SQLite after writer admission.
					_, cpErr = checkpoint.ExecContext(ctx, `BEGIN`)
					if cpErr == nil {
						cpErr = checkpoint.QueryRowContext(ctx, `SELECT v FROM mechanism WHERE id=1`).Scan(&value)
					}
				}
				if cpErr == nil {
					busy, frames, copied, cpErr = walCheckpoint(ctx, checkpoint, mode)
				}
				if tc.queryError {
					_, _ = checkpoint.ExecContext(context.Background(), `ROLLBACK`)
				}
				cpEnd = time.Now()
			}()
			// Always release the pinned reader and join the sole worker, including
			// fatal/skip paths, before closing any connection.
			defer func() { releaseReader(); cancel(); <-done }()
			<-entered
			if mode == "PASSIVE" || tc.queryError {
				<-done // baseline: reader still pinned, passive checkpoint finished
			} else if tc.ungated {
				// Give the checkpoint a scheduling opportunity. Only the observed
				// driver error and overlapping timestamps below establish overlap.
				time.Sleep(10 * time.Millisecond)
			} else {
				// Only the checkpoint can own the gate at this barrier: no
				// writer has started. Observe admission before queueing it.
				for len(gate.slot) == 0 {
					select {
					case <-done:
						t.Skip("INCONCLUSIVE: checkpoint ended before admission observed")
					case <-ctx.Done():
						t.Fatal(ctx.Err())
					case <-time.After(time.Millisecond):
					}
				}
			}
			var original driver.Conn
			var beginStart time.Time
			var gateHeld, checkpointPending bool
			if err := writer.Raw(func(raw any) error {
				c := raw.(*stmtConn)
				gate, original = c.gate, c.inner
				c.inner = &checkpointBeginProbe{Conn: original, before: func() {
					beginStart = time.Now()
					gateHeld = len(gate.slot) == 1
					select {
					case <-done:
						checkpointPending = false
					default:
						checkpointPending = true
					}
				}}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			defer writer.Raw(func(raw any) error { raw.(*stmtConn).inner = original; return nil })
			var beginErr, writeErr error
			var callStart, beginEnd time.Time
			var execTime, commitTime time.Duration
			writerEntered, writerDone := make(chan struct{}), make(chan struct{})
			go func() {
				defer close(writerDone)
				callStart = time.Now()
				close(writerEntered)
				var tx *sql.Tx
				tx, beginErr = writer.BeginTx(ctx, nil)
				beginEnd = time.Now()
				if beginErr == nil {
					defer tx.Rollback()
					start := time.Now()
					_, writeErr = tx.ExecContext(ctx, `UPDATE mechanism SET v='control' WHERE id=1`)
					execTime = time.Since(start)
					if writeErr == nil {
						start = time.Now()
						writeErr = tx.Commit()
						commitTime = time.Since(start)
					}
				}
			}()
			defer func() { releaseReader(); cancel(); <-writerDone }()
			<-writerEntered
			if tc.releaseEarly {
				time.Sleep(10 * time.Millisecond)
				releaseReader()
			}
			<-writerDone
			gateReleased := len(gate.slot) == 0
			<-done
			t.Logf("checkpoint=%s duration=%s busy=%d frames=%d copied=%d error=%v; admission=%s driver_BEGIN=%s gate_held=%v checkpoint_pending_at_BEGIN=%v error=%v; Exec=%s Commit=%s write_error=%v gate_released=%v",
				mode, cpEnd.Sub(cpStart), busy, frames, copied, cpErr, beginStart.Sub(callStart), beginEnd.Sub(beginStart), gateHeld, checkpointPending, beginErr, execTime, commitTime, writeErr, gateReleased)
			if (cpErr != nil && !tc.queryError) || !gateHeld || !gateReleased || writeErr != nil {
				t.Fatalf("invalid mechanism observation: checkpoint error=%v gate held=%v released=%v", cpErr, gateHeld, gateReleased)
			}
			if mode == "PASSIVE" {
				if beginErr != nil || busy != 0 || frames <= copied {
					t.Fatalf("pinned-reader PASSIVE control failed: begin=%v busy=%d frames=%d copied=%d", beginErr, busy, frames, copied)
				}
			} else if tc.ungated {
				var native interface{ Code() int }
				isNativeBusy := errors.As(beginErr, &native) && native.Code()&255 == 5
				overlap := checkpointPending && beginStart.After(cpStart) && beginEnd.Before(cpEnd)
				if !isNativeBusy || !overlap {
					t.Skipf("INCONCLUSIVE: no positive native-BUSY/checkpoint overlap: native_busy=%v overlap=%v", isNativeBusy, overlap)
				}
				if errors.Is(beginErr, errWriteGateTimeout) || busy != 1 {
					t.Fatalf("wrong contention stage: begin=%v checkpoint busy=%d", beginErr, busy)
				}
				t.Log("POSITIVE: writer held Go gate while underlying BEGIN returned native SQLITE_BUSY entirely within TRUNCATE call; Exec/Commit were not reached")
			} else {
				if beginErr != nil {
					t.Fatalf("admitted writer rejected: %v", beginErr)
				}
				if tc.queryError {
					if cpErr == nil {
						t.Fatal("expected checkpoint error in read transaction")
					}
				} else if tc.releaseEarly && busy != 0 || !tc.releaseEarly && !tc.queryError && busy != 1 {
					t.Fatalf("unexpected checkpoint busy=%d", busy)
				}
				t.Log("ADMITTED: rejected writer operations=0; BEGIN/Exec/Commit succeeded after checkpoint released admission; no touched-title preservation claim")
			}
			releaseReader()
			// Check recovery after the checkpoint has ended and reader is gone.
			tx, err := writer.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := tx.ExecContext(ctx, `UPDATE mechanism SET v='recovered' WHERE id=1`); err != nil {
				_ = tx.Rollback()
				t.Fatal(err)
			}
			if err := tx.Commit(); err != nil {
				t.Fatal(err)
			}
			t.Log("recovery BEGIN/Exec/Commit succeeded; one-row database, checkpoint goroutine joined")
		})
	}
}

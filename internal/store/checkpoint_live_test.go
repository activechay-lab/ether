package store

import (
	"context"
	"database/sql/driver"
	"fmt"
	"strings"
	"testing"
)

// The old idle check selects TRUNCATE in this exact single-connection case.
// Keep the real PASSIVE driver operation and reject any strong-checkpoint SQL.
func TestLiveCheckpointNeverSelectsStrongMode(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	if _, err := db.ExecContext(ctx, `CREATE TABLE checkpoint_live(v INTEGER); INSERT INTO checkpoint_live VALUES(1)`); err != nil {
		t.Fatal(err)
	}
	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var probe *liveCheckpointProbe
	if err := conn.Raw(func(raw any) error {
		c := raw.(*stmtConn)
		probe = &liveCheckpointProbe{Conn: c.inner}
		c.inner = probe
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
	ResetWriteGateHolds()
	if err := Checkpoint(ctx, db, true); err != nil {
		t.Fatal(err)
	}
	if probe.passive != 1 || probe.strong != 0 {
		t.Fatalf("passive=%d strong=%d", probe.passive, probe.strong)
	}
	if got := ReadWriteGateHolds(); got.Holds != 0 {
		t.Fatalf("checkpoint acquired writer admission: %+v", got)
	}
	if _, err := db.ExecContext(ctx, `UPDATE checkpoint_live SET v=2`); err != nil {
		t.Fatal(err)
	}
	var v int
	if err := db.QueryRowContext(ctx, `SELECT v FROM checkpoint_live`).Scan(&v); err != nil || v != 2 {
		t.Fatalf("following write: v=%d err=%v", v, err)
	}
	if got := ReadWriteGateHolds(); got.Holds != 1 {
		t.Fatalf("ordinary write admission lost: %+v", got)
	}
}

type liveCheckpointProbe struct {
	driver.Conn
	passive, strong int
}

func (p *liveCheckpointProbe) Prepare(query string) (driver.Stmt, error) {
	u := strings.ToUpper(query)
	if strings.Contains(u, "WAL_CHECKPOINT") {
		if u != "PRAGMA WAL_CHECKPOINT(PASSIVE)" {
			p.strong++
			return nil, fmt.Errorf("live pool selected strong checkpoint: %s", query)
		}
		p.passive++
	}
	return p.Conn.Prepare(query)
}

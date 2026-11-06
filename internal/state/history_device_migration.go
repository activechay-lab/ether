package state

import "github.com/braedonsaunders/ether/internal/store"

func init() {
	// state/8's Go callback bypassed DDL translation and created an int4
	// device_id on PostgreSQL. Device registry identities are 64-bit.
	// SQLite already stores these values as signed 64-bit integers.
	store.Register("state", 10, `SELECT 1;`)
	store.RegisterDialect("state", 10, store.Postgres, `ALTER TABLE state_history ALTER COLUMN device_id TYPE BIGINT;`)
}

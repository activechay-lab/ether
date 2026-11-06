package placement

import (
	"context"
	"database/sql"
	"encoding/json"

	"github.com/braedonsaunders/ether/internal/store"
)

func init() {
	store.Register("placement", 4, `
CREATE TABLE placement_displaced (
 session TEXT NOT NULL, generation BIGINT NOT NULL, record TEXT NOT NULL,
 PRIMARY KEY(session,generation));
CREATE TABLE placement_displaced_claims (
 session TEXT NOT NULL, generation BIGINT NOT NULL, scope TEXT NOT NULL, units BIGINT NOT NULL,
 PRIMARY KEY(session,generation,scope));
CREATE INDEX placement_displaced_scope ON placement_displaced_claims(scope);
`)
}

// Supersede revokes an execution without asserting its process has retired.
// Old capacity remains charged independently of the replacement. The trusted
// controller callback seals its operation and installs its route in this same
// transaction. An unknown outcome must retry the identical request.
func (s *Service) Supersede(ctx context.Context, failed Fence, request Request, seal func(context.Context, *sql.Tx, Record) error) (Record, error) {
	var out Record
	r, hash, err := normalize(request, s.options.MaxLease)
	if err != nil {
		return out, err
	}
	if !validFence(failed) || r.Session != failed.Session || r.ExpectedGeneration != failed.Generation || r.Target.NodeID == failed.Target.NodeID || r.Controller != failed.Controller || seal == nil {
		return out, ErrInvalid
	}
	err = s.transaction(ctx, func(ctx context.Context, tx *sql.Tx) error {
		old, oldHash, err := read(ctx, tx, r.Session)
		if err != nil {
			return err
		}
		if old.Operation == r.Operation && old.Generation == failed.Generation+1 {
			if oldHash != hash {
				return ErrConflict
			}
			out = old
			return nil
		}
		if old.Fence != failed || old.Retired {
			return ErrFenced
		}
		// Bound retained failure history even if a controller repeatedly partitions.
		var count int
		if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM placement_displaced`).Scan(&count); err != nil {
			return err
		}
		if count >= s.options.MaxSessions {
			return ErrQuota
		}
		raw, err := json.Marshal(old)
		if err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO placement_displaced(session,generation,record) VALUES(?,?,?)`, old.Session, old.Generation, string(raw)); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO placement_displaced_claims(session,generation,scope,units) SELECT session,?,scope,units FROM placement_claims WHERE session=?`, old.Generation, old.Session); err != nil {
			return err
		}
		if err = retireRecord(ctx, tx, old); err != nil {
			return err
		} // moves accounting, NOT retirement evidence
		if err = s.reserveInTx(ctx, tx, r, hash, &out); err != nil {
			return err
		}
		return seal(ctx, tx, out)
	})
	return out, err
}

// RetireDisplaced releases only an exactly verified former execution. Silence,
// missing membership and lease expiry are never accepted as process exit proof.
func (s *Service) RetireDisplaced(ctx context.Context, f Fence, evidence string) error {
	if !validFence(f) || !valid(evidence) || s.verifier == nil {
		return ErrRetirement
	}
	if err := s.verifier.VerifyRetired(ctx, f, evidence); err != nil {
		return err
	}
	return s.transaction(ctx, func(ctx context.Context, tx *sql.Tx) error {
		var raw string
		err := tx.QueryRowContext(ctx, `SELECT record FROM placement_displaced WHERE session=? AND generation=?`, f.Session, f.Generation).Scan(&raw)
		if err == sql.ErrNoRows {
			return nil
		}
		if err != nil {
			return err
		}
		var old Record
		if json.Unmarshal([]byte(raw), &old) != nil || old.Fence != f {
			return ErrFenced
		}
		if _, err = tx.ExecContext(ctx, `DELETE FROM placement_displaced_claims WHERE session=? AND generation=?`, f.Session, f.Generation); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `DELETE FROM placement_displaced WHERE session=? AND generation=?`, f.Session, f.Generation)
		return err
	})
}

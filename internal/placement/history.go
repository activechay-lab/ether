package placement

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"time"
)

const ErrReceiptUnknown Error = "placement: exact retirement receipt unknown"

// HistoryKey identifies one exact generation in the configured cluster. A higher
// generation or a missing row is never an exact historical retirement receipt.
type HistoryKey struct {
	Cluster string
	Fence   Fence
}

// RenewalSummary preserves only the last worker-accepted CAS, never source-only
// extension evidence. It grants no authority and cannot be replayed as a renewal.
type RenewalSummary struct {
	Operation, PreviousVersion uint64
	PreviousDeadline, Deadline time.Time
}

// RetirementReceipt contains bounded identifiers and SHA-256 digests, never raw
// credentials, capabilities, URLs or proof payloads. RequestHash commits the exact
// immutable worker Start encoding. NeverStarted is reserved for authenticated
// stop-before-start proof whose admission barrier was sealed before attestation;
// it is not inferred from missing records. All three proofs remain mandatory.
type RetirementReceipt struct {
	Key                                   HistoryKey
	Operation                             string // placement reservation operation
	RequestHash                           [32]byte
	NeverStarted                          bool
	Source                                Target
	PlayID, Revision                      string
	Size                                  int64
	EncoderProof, InputProof, SourceProof [32]byte
	InitialDeadline                       time.Time
	Renewable                             bool
	Renewal                               *RenewalSummary
}

// ReceiptVerifier is a trusted host dependency, not a wire decoder. It MUST bind
// Key.Cluster to the actual configured cluster authority, authenticate the full
// fence, request hash and source owner/PlayID/revision/size, and independently
// establish exact encoder AND local input AND source-owner retirement. Source
// proof includes backend Done+nil Err and joined producers, not merely zero holds.
// NeverStarted requires an irreversible exact negative admission tombstone.
// Accepted renewal history must be worker-accepted, not just source-extended.
// Digests alone, silence, expiry and missing status are never proof. Verification
// runs outside placement locks; callers must retain ownership through publication.
type ReceiptVerifier interface {
	VerifyReceipt(context.Context, RetirementReceipt) error
}

// History shares its explicitly supplied placement Service's DB and transaction
// policy. It owns no workers or goroutines. The namespace is trusted host config,
// never a caller request field; the DB also pins a single namespace for this ledger.
type History struct {
	placement *Service
	cluster   string
	verifier  ReceiptVerifier
}

func NewHistory(service *Service, configuredCluster string, verifier ReceiptVerifier) (*History, error) {
	if service == nil || service.db == nil || !valid(configuredCluster) || verifier == nil {
		return nil, ErrInvalid
	}
	return &History{placement: service, cluster: configuredCluster, verifier: verifier}, nil
}

func copyReceipt(r RetirementReceipt) RetirementReceipt {
	if r.Renewal != nil {
		n := *r.Renewal
		r.Renewal = &n
	}
	return r
}
func canonicalTime(t time.Time) time.Time {
	if t.IsZero() {
		return time.Time{}
	}
	return t.UTC().Round(0)
}
func (h *History) normalize(r RetirementReceipt) (RetirementReceipt, string, error) {
	r = copyReceipt(r)
	zero := [32]byte{}
	if r.Key.Cluster != h.cluster || !validFence(r.Key.Fence) || !valid(r.Operation) || !validTarget(r.Source) || !valid(r.PlayID) || !valid(r.Revision) || r.Size < 0 || r.EncoderProof == zero || r.InputProof == zero || r.SourceProof == zero {
		return r, "", ErrInvalid
	}
	r.InitialDeadline = canonicalTime(r.InitialDeadline)
	if r.NeverStarted {
		if r.RequestHash != zero || !r.InitialDeadline.IsZero() || r.Renewal != nil || r.Renewable {
			return r, "", ErrInvalid
		}
	} else if r.RequestHash == zero || r.InitialDeadline.IsZero() {
		return r, "", ErrInvalid
	}
	if n := r.Renewal; n != nil {
		n.PreviousDeadline, n.Deadline = canonicalTime(n.PreviousDeadline), canonicalTime(n.Deadline)
		if !r.Renewable || n.Operation == 0 || n.PreviousVersion == math.MaxUint64 || n.Operation != n.PreviousVersion+1 || n.PreviousDeadline.Before(r.InitialDeadline) || !n.Deadline.After(n.PreviousDeadline) || (n.PreviousVersion == 0 && !n.PreviousDeadline.Equal(r.InitialDeadline)) {
			return r, "", ErrInvalid
		}
	}
	data, err := json.Marshal(r)
	if err != nil || len(data) > 16384 {
		return r, "", ErrInvalid
	}
	return r, string(data), nil
}
func (h *History) namespace(ctx context.Context, tx *sql.Tx, seed bool) error {
	if seed {
		if _, err := tx.ExecContext(ctx, `INSERT INTO placement_history_namespace(id,cluster) VALUES(1,?) ON CONFLICT(id) DO NOTHING`, h.cluster); err != nil {
			return err
		}
	}
	var cluster string
	err := tx.QueryRowContext(ctx, `SELECT cluster FROM placement_history_namespace WHERE id=1`).Scan(&cluster)
	if err == sql.ErrNoRows {
		return ErrReceiptUnknown
	}
	if err != nil {
		return err
	}
	if cluster != h.cluster {
		return ErrPolicy
	}
	return nil
}
func receiptArgs(k HistoryKey) []any {
	f := k.Fence
	return []any{k.Cluster, f.Session, f.Generation, f.Controller.ID, f.Controller.Boot, f.Target.NodeID, f.Target.Boot}
}
func readReceipt(ctx context.Context, tx *sql.Tx, k HistoryKey) (string, error) {
	var raw string
	err := tx.QueryRowContext(ctx, `SELECT receipt FROM placement_history_receipts WHERE cluster=? AND session=? AND generation=? AND controller=? AND controller_boot=? AND node=? AND boot=?`, receiptArgs(k)...).Scan(&raw)
	return raw, err
}

// Lookup returns exact persisted trusted evidence or unknown. It neither infers
// retirement from a generation floor nor supplies current execution authority.
func (h *History) Lookup(ctx context.Context, k HistoryKey) (RetirementReceipt, error) {
	var out RetirementReceipt
	if k.Cluster != h.cluster || !validFence(k.Fence) {
		return out, ErrInvalid
	}
	err := h.placement.transaction(ctx, func(ctx context.Context, tx *sql.Tx) error {
		if err := h.namespace(ctx, tx, false); err != nil {
			return err
		}
		raw, err := readReceipt(ctx, tx, k)
		if err == sql.ErrNoRows {
			return ErrReceiptUnknown
		}
		if err != nil {
			return err
		}
		if len(raw) > 16384 || json.Unmarshal([]byte(raw), &out) != nil || out.Key != k {
			return ErrConflict
		}
		normalized, encoded, err := h.normalize(out)
		if err != nil || encoded != raw {
			return ErrConflict
		}
		out = normalized
		return nil
	})
	if err != nil {
		return RetirementReceipt{}, err
	}
	return out, nil
}

// Record publishes evidence only for an already retired current exact fence. It
// cannot release claims. If replacement wins first, it fails closed. Prefer
// RetireWithReceipt to avoid that publication/replacement gap. Immutable replay
// of an existing trusted receipt succeeds after replacement without re-verifying
// discarded local payloads. Conflicting replay is always rejected.
func (h *History) Record(ctx context.Context, r RetirementReceipt) error {
	return h.publish(ctx, r, "", false)
}

// RetireWithReceipt invokes BOTH trusted verifiers before taking placement:all,
// then atomically rechecks the full current fence and persists the receipt with
// the normal Retire claim release. An existing immutable receipt is read-only
// reconciliation and needs no fresh proof. UnknownOutcome requires exact Lookup;
// never discard local ownership on an unconfirmed result. No quota/floor is freed.
func (h *History) RetireWithReceipt(ctx context.Context, r RetirementReceipt, evidence string) error {
	if !valid(evidence) {
		return ErrInvalid
	}
	return h.publish(ctx, r, evidence, true)
}
func (h *History) publish(ctx context.Context, r RetirementReceipt, evidence string, retire bool) error {
	r, raw, err := h.normalize(r)
	if err != nil {
		return err
	}
	// Fast replay is a read of an immutable authenticated result, not a new write.
	old, err := h.Lookup(ctx, r.Key)
	if err == nil {
		_, encoded, _ := h.normalize(old)
		if encoded != raw {
			return ErrConflict
		}
		return nil
	}
	if err != ErrReceiptUnknown {
		return err
	}
	s := h.placement
	if retire {
		if s.verifier == nil {
			return ErrRetirement
		}
		if err := s.verifier.VerifyRetired(ctx, r.Key.Fence, evidence); err != nil {
			return fmt.Errorf("%w: %v", ErrRetirement, err)
		}
	}
	if err := h.verifier.VerifyReceipt(ctx, copyReceipt(r)); err != nil {
		return fmt.Errorf("%w: %v", ErrRetirement, err)
	}
	return s.transaction(ctx, func(ctx context.Context, tx *sql.Tx) error {
		if err := h.namespace(ctx, tx, true); err != nil {
			return err
		}
		existing, err := readReceipt(ctx, tx, r.Key)
		if err == nil {
			if existing != raw {
				return ErrConflict
			}
			return nil
		}
		if err != sql.ErrNoRows {
			return err
		}
		current, _, err := read(ctx, tx, r.Key.Fence.Session)
		if err == sql.ErrNoRows {
			return ErrFenced
		}
		if err != nil {
			return err
		}
		if current.Fence != r.Key.Fence {
			return ErrFenced
		}
		if current.Operation != r.Operation {
			return ErrConflict
		}
		if !retire && !current.Retired {
			return ErrRetirement
		}
		args := append(receiptArgs(r.Key), raw)
		if _, err = tx.ExecContext(ctx, `INSERT INTO placement_history_receipts(cluster,session,generation,controller,controller_boot,node,boot,receipt) VALUES(?,?,?,?,?,?,?,?)`, args...); err != nil {
			return err
		}
		if retire {
			return s.retireRecord(ctx, tx, current)
		}
		return nil
	})
}

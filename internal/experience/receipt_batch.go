package experience

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"database/sql"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/handles/capability"
	"github.com/braedonsaunders/ether/internal/store"
	"github.com/braedonsaunders/ether/internal/viewerpolicy"
)

// receiptInsertRows bounds one receipt INSERT's rows (11 parameters each).
const receiptInsertRows = 50

// IssueBatch atomically issues receipts and shared admission in input order.
func (s *Receipts) IssueBatch(ctx context.Context, viewer *core.User, device core.Device, protocol core.Protocol, subjects []ReceiptSubject) ([]string, error) {
	ctx = core.WithCatalogViewer(ctx, viewer)
	return s.issueBatchChecked(ctx, viewer, device, protocol, subjects, nil, nil)
}

func (s *Receipts) issueBatchChecked(ctx context.Context, viewer *core.User, device core.Device, protocol core.Protocol, subjects []ReceiptSubject, current func() bool, fence func(*sql.Tx) error) ([]string, error) {
	ctx = core.WithCatalogViewer(ctx, viewer)
	for attempt := 0; ; attempt++ {
		tokens, err := s.issueBatchAttempt(ctx, viewer, device, protocol, subjects, current, fence)
		if !errors.Is(err, viewerpolicy.ErrChanged) || attempt == 1 {
			return tokens, err
		}
	}
}

func (s *Receipts) issueBatchAttempt(ctx context.Context, viewer *core.User, device core.Device, protocol core.Protocol, subjects []ReceiptSubject, current func() bool, fence func(*sql.Tx) error) ([]string, error) {
	ctx = core.WithCatalogViewer(ctx, viewer)
	if s.opts.PolicyLease != nil {
		release, err := s.opts.PolicyLease(ctx)
		if err != nil {
			return nil, err
		}
		defer release()
	}
	epoch, err := viewerpolicy.Read(ctx, s.opts.DB)
	if err != nil {
		return nil, err
	}
	if viewer == nil || viewer.ID <= 0 || device.ID == "" || protocol == "" || len(subjects) == 0 || len(subjects) > s.opts.MaxPerDevice {
		return nil, ErrReceipt
	}
	snapshots := make([]string, len(subjects))
	encoded := make(map[*core.NeutralImpression]string)
	for i, subject := range subjects {
		if subject.Kind == "" || subject.ID == "" {
			return nil, ErrReceipt
		}
		if imp := subject.Impression; imp != nil {
			if !snapshotMatches(subject, imp) || len(imp.Items) > 1000 || len(imp.Entries) > 1000 || len(imp.Placement) > 128 || len(imp.ViewID) > 512 || len(imp.RowID) > 512 || len(imp.Kind) > 512 || len(imp.Title) > 2048 {
				return nil, ErrReceipt
			}
			value, ok := encoded[imp]
			if !ok {
				b, err := json.Marshal(imp)
				if err != nil || len(b) > 32768 {
					return nil, ErrReceipt
				}
				value = string(b)
				encoded[imp] = value
			}
			snapshots[i] = value
		}
	}
	active, err := s.opts.Active(ctx, viewer.ID)
	if err != nil || active == nil || active.ID != viewer.ID {
		return nil, ErrReceipt
	}
	if s.opts.Prefetch != nil {
		// The titles the subjects and their rows' entries name.
		var keys []core.RatingKey
		seen := map[string]bool{}
		add := func(kind, id string) {
			if kind != string(frontend.RowEntryTitle) || seen[id] {
				return
			}
			seen[id] = true
			if key, err := core.ParseRatingKey(id); err == nil {
				keys = append(keys, key)
			}
		}
		for _, subject := range subjects {
			add(subject.Kind, subject.ID)
			if imp := subject.Impression; imp != nil {
				for _, e := range imp.Entries {
					add(string(e.Kind), e.ID)
				}
			}
		}
		if len(keys) > 1 {
			s.opts.Prefetch(ctx, keys)
		}
	}
	for _, subject := range subjects {
		if !s.opts.Allowed(ctx, active, device, protocol, subject) {
			return nil, ErrReceipt
		}
	}
	var generation int64
	if s.opts.Generation != nil {
		generation, err = s.opts.Generation(ctx, active, device, protocol)
		if err != nil || generation < 0 {
			return nil, ErrReceipt
		}
	}
	if current != nil && !current() {
		return nil, ErrRowViewStale
	}
	tx, err := s.opts.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if err = capability.LockHeaderScopeInTx(ctx, s.opts.DB, tx, capability.HeaderScope{Namespace: ReceiptCapabilityNamespace, ViewerID: viewer.ID, Surface: string(protocol), DeviceID: device.ID}); err != nil {
		return nil, err
	}
	if err = store.LockKey(ctx, store.DialectOf(s.opts.DB), tx, fmt.Sprintf("experience:receipts:%d:%s", viewer.ID, device.ID)); err != nil {
		return nil, err
	}
	if err = viewerpolicy.Fence(ctx, s.opts.DB, tx, epoch); err != nil {
		return nil, err
	}
	now := s.opts.Now().Unix()
	if err = s.sweepExpired(ctx, tx, viewer.ID, device.ID, now); err != nil {
		return nil, err
	}
	var count int
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM experience_receipts WHERE viewer_id=? AND device_id=? AND expires>?`, viewer.ID, device.ID, now).Scan(&count); err != nil {
		return nil, err
	}
	if count+len(subjects) > s.opts.MaxPerDevice {
		return nil, errors.New("experience: receipt limit reached")
	}
	tokens := make([]string, len(subjects))
	hashes := make([]string, len(subjects))
	expires := s.opts.Now().Add(s.opts.TTL).Unix()
	millis, e := receiptExpiresMillis(expires)
	if e != nil {
		return nil, e
	}
	for i := range subjects {
		tokens[i], hashes[i], err = s.codec.Issue()
		if err != nil {
			return nil, err
		}
	}
	for start := 0; start < len(subjects); start += capability.HeaderBatchMax {
		end := min(start+capability.HeaderBatchMax, len(subjects))
		headers := make([]capability.Header, 0, end-start)
		for _, hash := range hashes[start:end] {
			headers = append(headers, capability.Header{HeaderScope: capability.HeaderScope{Namespace: ReceiptCapabilityNamespace, ViewerID: viewer.ID, Surface: string(protocol), DeviceID: device.ID}, Digest: hash, ExpiresMillis: millis})
		}
		if err = s.bindHeaders(ctx, tx, headers); err != nil {
			return nil, err
		}
	}
	// One multi-row INSERT per receiptInsertRows subjects, not one per
	// subject: a Silo home section issues a receipt for every card.
	for start := 0; start < len(subjects); start += receiptInsertRows {
		end := min(start+receiptInsertRows, len(subjects))
		q := `INSERT INTO experience_receipts(hash,viewer_id,device_id,protocol,expires,kind,subject,purpose,rank,impression,generation) VALUES ` +
			strings.TrimSuffix(strings.Repeat(`(?,?,?,?,?,?,?,?,?,?,?),`, end-start), ",")
		args := make([]any, 0, 11*(end-start))
		for i := start; i < end; i++ {
			subject := subjects[i]
			args = append(args, hashes[i], viewer.ID, device.ID, string(protocol), expires, subject.Kind, subject.ID, string(subject.Purpose), subject.Rank, snapshots[i], generation)
		}
		if _, err = tx.ExecContext(ctx, q, args...); err != nil {
			return nil, err
		}
	}
	if err = viewerpolicy.Fence(ctx, s.opts.DB, tx, epoch); err != nil {
		return nil, err
	}
	if fence != nil {
		if err = fence(tx); err != nil {
			return nil, err
		}
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return tokens, nil
}

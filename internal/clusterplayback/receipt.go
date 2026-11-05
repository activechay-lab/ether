package clusterplayback

import (
	"context"
	"encoding/json"
	"github.com/braedonsaunders/ether/internal/placement"
	"github.com/braedonsaunders/ether/internal/remotetranscode"
)

func (r *Runtime) collectReceipt(ctx context.Context, route Route) (placement.RetirementReceipt, error) {
	record, e := r.o.Placement.Lookup(ctx, route.Fence.Session)
	if e != nil || record.Fence != route.Fence {
		return placement.RetirementReceipt{}, ErrDenied
	}
	encoder, e := r.evidenceCall(ctx, route, "encoder")
	if e != nil || encoder.Worker == nil {
		return placement.RetirementReceipt{}, errEvidenceUnsupported
	}
	source, e := r.evidenceCall(ctx, route, "source")
	if e != nil || source.Source == nil {
		return placement.RetirementReceipt{}, ErrPending
	}
	summary, proof := encoder.Worker, source.Source
	encoderDigest, e := summary.EncoderDigest()
	if e != nil {
		return placement.RetirementReceipt{}, e
	}
	inputDigest, e := summary.InputDigest()
	if e != nil {
		return placement.RetirementReceipt{}, e
	}
	sourceDigest, e := proof.digest()
	if e != nil {
		return placement.RetirementReceipt{}, e
	}
	receipt := placement.RetirementReceipt{Key: placement.HistoryKey{Cluster: r.o.Self.Cluster, Fence: route.Fence}, Operation: record.Operation, RequestHash: summary.StartHash, NeverStarted: summary.Origin == remotetranscode.EvidenceStopBeforeStart, Source: placement.Target{NodeID: route.Source.NodeID, Boot: route.Source.Boot}, PlayID: route.PlayID, Revision: route.Revision, Size: proof.Size, EncoderProof: encoderDigest, InputProof: inputDigest, SourceProof: sourceDigest, InitialDeadline: summary.InitialDeadline, Renewable: summary.Renewable}
	if accepted := summary.AcceptedRenewal; accepted != nil {
		receipt.Renewal = &placement.RenewalSummary{Operation: accepted.Operation, PreviousVersion: accepted.PreviousVersion, PreviousDeadline: accepted.PreviousDeadline, Deadline: accepted.Deadline}
	}
	return receipt, nil
}

// VerifyReceipt authenticates fresh independent worker/input and source proof.
// Digests are compared only after authenticated exact-peer observations.
func (r *Runtime) VerifyReceipt(ctx context.Context, receipt placement.RetirementReceipt) error {
	if receipt.Key.Cluster != r.o.Self.Cluster {
		return ErrDenied
	}
	route, e := r.route(ctx, receipt.Key.Fence.Session)
	if e != nil || route.Fence != receipt.Key.Fence {
		return ErrDenied
	}
	fresh, e := r.collectReceipt(ctx, route)
	if e != nil {
		return e
	}
	a, e := json.Marshal(fresh)
	if e != nil {
		return e
	}
	b, e := json.Marshal(receipt)
	if e != nil || string(a) != string(b) {
		return ErrDenied
	}
	return nil
}
func (r *Runtime) retireWithEvidence(ctx context.Context, route Route, evidence string) error {
	if retired, e := r.retainedReceipt(ctx, route); e != nil {
		return e
	} else if retired {
		return nil
	}
	receipt, e := r.collectReceipt(ctx, route)
	if e != nil {
		// Ordinary retirement still requires its existing verifier. Unsupported
		// receipt evidence NEVER creates history or allows payload compaction.
		return r.o.Placement.Retire(ctx, route.Fence, evidence)
	}
	// An unknown atomic publication result remains owned; retry exact Lookup on
	// the next Tick. Never fall back to non-receipt retirement after this call.
	return r.history.RetireWithReceipt(ctx, receipt, evidence)
}

var _ placement.ReceiptVerifier = (*Runtime)(nil)

func (r *Runtime) retainedReceipt(ctx context.Context, route Route) (bool, error) {
	key := placement.HistoryKey{Cluster: r.o.Self.Cluster, Fence: route.Fence}
	retained, e := r.history.Lookup(ctx, key)
	if e == placement.ErrReceiptUnknown {
		return false, nil
	}
	if e != nil {
		return false, e
	}
	if retained.Source != (placement.Target{NodeID: route.Source.NodeID, Boot: route.Source.Boot}) || retained.PlayID != route.PlayID || retained.Revision != route.Revision {
		return false, ErrDenied
	}
	return true, nil
}

// LookupRetirementReceipt returns immutable exact historical proof in this
// configured cluster. It never grants execution or infers proof from a floor.
func (r *Runtime) LookupRetirementReceipt(ctx context.Context, f placement.Fence) (placement.RetirementReceipt, error) {
	return r.history.Lookup(ctx, placement.HistoryKey{Cluster: r.o.Self.Cluster, Fence: f})
}

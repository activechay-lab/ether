package remotetranscode

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

type EvidenceOrigin uint8

const (
	EvidenceOriginUnknown EvidenceOrigin = iota
	EvidenceAcceptedStart
	EvidenceStopBeforeStart
)

type TerminalProofKind uint8

const (
	TerminalProofUnknown TerminalProofKind = iota
	TerminalProofNilObjects
	TerminalProofNegativeBarrier
)

// TerminalEvidenceSummary is immutable local proof data, not execution authority
// or source-owner proof. Digests commit authenticated observations; they cannot
// authenticate themselves. Runtime must verify the exact full durable fence and
// current intended worker peer independently before publishing a receipt.
type TerminalEvidenceSummary struct {
	Version                                     uint8
	Identity                                    Identity
	Origin                                      EvidenceOrigin
	StartHash                                   [32]byte
	InitialDeadline                             time.Time
	Renewable                                   bool
	LeaseVersion                                uint64
	LeaseDeadline                               time.Time
	AcceptedRenewal                             *Renewal
	EncoderProof                                TerminalProofKind
	BackendInvoked                              bool
	BackendTokenJoined, OwnedSessionTokenJoined bool
	InputProof                                  TerminalProofKind
	// Slots are the original ordinary/gateway input leases, never source-owner proof.
	InputPresent              [2]bool
	InputNilTerminal          [2]bool
	NegativeBarrierGeneration uint64
}

// Digest produces a bounded versioned commitment, never an authenticity proof.
// The separate domains let a trusted receipt provider bind independently checked
// encoder and local-input observations without substituting a source-owner proof.
func (e TerminalEvidenceSummary) Digest() ([32]byte, error) {
	return e.digest("ether/remote-terminal-evidence/v1")
}
func (e TerminalEvidenceSummary) EncoderDigest() ([32]byte, error) {
	return e.digest("ether/remote-encoder-proof/v1")
}
func (e TerminalEvidenceSummary) InputDigest() ([32]byte, error) {
	return e.digest("ether/remote-local-input-proof/v1")
}
func (e TerminalEvidenceSummary) digest(domain string) ([32]byte, error) {
	if !validTerminalEvidence(e) {
		return [32]byte{}, ErrUnsupported
	}
	b, err := json.Marshal(e)
	if err != nil || len(b) > 4096 {
		return [32]byte{}, ErrUnsupported
	}
	return sha256.Sum256(append([]byte(domain+"\x00"), b...)), nil
}
func validTerminalEvidence(e TerminalEvidenceSummary) bool {
	if e.Version != 1 || !validIdentity(e.Identity) {
		return false
	}
	if e.Origin == EvidenceStopBeforeStart {
		return e.StartHash == ([32]byte{}) && e.InitialDeadline.IsZero() && !e.Renewable && e.LeaseVersion == 0 && e.LeaseDeadline.IsZero() && e.AcceptedRenewal == nil && e.EncoderProof == TerminalProofNegativeBarrier && e.InputProof == TerminalProofNegativeBarrier && !e.BackendInvoked && !e.BackendTokenJoined && !e.OwnedSessionTokenJoined && e.InputPresent == ([2]bool{}) && e.InputNilTerminal == ([2]bool{}) && e.NegativeBarrierGeneration == e.Identity.Generation
	}
	if e.Origin != EvidenceAcceptedStart || e.StartHash == ([32]byte{}) || e.InitialDeadline.IsZero() || e.LeaseDeadline.Before(e.InitialDeadline) || e.EncoderProof != TerminalProofNilObjects || e.InputProof != TerminalProofNilObjects || !e.BackendInvoked || !e.BackendTokenJoined || !e.OwnedSessionTokenJoined || e.NegativeBarrierGeneration != 0 {
		return false
	}
	if e.InputPresent == ([2]bool{}) || e.InputPresent != e.InputNilTerminal {
		return false
	}
	if e.LeaseVersion == 0 {
		return e.AcceptedRenewal == nil && e.LeaseDeadline.Equal(e.InitialDeadline)
	}
	n := e.AcceptedRenewal
	return e.Renewable && n != nil && validRenewal(*n) && n.Identity == e.Identity && n.Operation == e.LeaseVersion && n.Deadline.Equal(e.LeaseDeadline) && !n.PreviousDeadline.Before(e.InitialDeadline) && (n.PreviousVersion != 0 || n.PreviousDeadline.Equal(e.InitialDeadline))
}

// TerminalEvidence reads retained provenance only. It never stops/expires work,
// creates a negative barrier, issues credentials or infers proof from absence.
// Caller first stops the exact execution through its separately authorized host
// Terminal API. Cancellation, pending callbacks/producers or nonnil terminal
// errors produce no evidence. Ambiguous accepted starts retain their history.
func (w *Worker) TerminalEvidence(ctx context.Context, id Identity) (TerminalEvidenceSummary, error) {
	if ctx == nil || !validIdentity(id) || id.Worker != w.opts.Boot {
		return TerminalEvidenceSummary{}, ErrUnsupported
	}
	if err := ctx.Err(); err != nil {
		return TerminalEvidenceSummary{}, err
	}
	w.mu.Lock()
	e := w.records[id]
	if e == nil || !e.stopped || e.starting || e.validatingRenewals != 0 {
		w.mu.Unlock()
		return TerminalEvidenceSummary{}, ErrUncertain
	}
	version := e.responsibilityVersion
	summary := TerminalEvidenceSummary{Version: 1, Identity: id, Origin: e.origin, StartHash: e.hash, InitialDeadline: e.start.Expires.UTC().Round(0), Renewable: e.lease.Renewable, LeaseVersion: e.lease.Version, LeaseDeadline: e.lease.Deadline.UTC().Round(0), NegativeBarrierGeneration: e.negativeBarrierGeneration}
	if e.lease.Accepted != nil {
		copy := *e.lease.Accepted
		summary.AcceptedRenewal = &copy
	}
	session, backend := e.session, e.retirement
	inputs := [2]core.EncoderInputLease{e.input, e.gatewayInput}
	invoked := e.backendInvoked
	acceptedValid := e.start.Identity == id && e.hash != ([32]byte{})
	negativeValid := e.origin == EvidenceStopBeforeStart && e.start.Identity == (Identity{}) && e.hash == ([32]byte{}) && e.negativeBarrierGeneration == id.Generation && w.highest[id.Controller+"/"+id.Session] >= id.Generation
	w.mu.Unlock()
	if summary.Origin == EvidenceStopBeforeStart {
		if !negativeValid || invoked || session != nil || backend != nil || inputs[0] != nil || inputs[1] != nil {
			return TerminalEvidenceSummary{}, ErrUnsupported
		}
		summary.EncoderProof, summary.InputProof = TerminalProofNegativeBarrier, TerminalProofNegativeBarrier
	} else if summary.Origin == EvidenceAcceptedStart {
		if !acceptedValid || !invoked || session == nil || backend == nil {
			return TerminalEvidenceSummary{}, ErrUnsupported
		}
		owned := session.EncoderRetirement()
		if owned == nil {
			return TerminalEvidenceSummary{}, ErrUnsupported
		}
		// Getter/proof calls run OUTSIDE the worker mutex. A stopped, fully joined
		// record freezes these retained references; the version recheck below prevents
		// publishing a proof if ownership was still being attached while sampled.
		for _, token := range []core.EncoderRetirement{backend, owned} {
			select {
			case <-token.EncoderDone():
				if token.EncoderErr() != nil {
					return TerminalEvidenceSummary{}, ErrUncertain
				}
			default:
				return TerminalEvidenceSummary{}, ErrUncertain
			}
		}
		summary.BackendInvoked = true
		summary.EncoderProof = TerminalProofNilObjects
		summary.BackendTokenJoined = true
		summary.OwnedSessionTokenJoined = true
		summary.InputProof = TerminalProofNilObjects
		for i, input := range inputs {
			if input == nil {
				continue
			}
			summary.InputPresent[i] = true
			select {
			case <-input.InputDone():
				if input.InputErr() != nil {
					return TerminalEvidenceSummary{}, ErrUncertain
				}
				summary.InputNilTerminal[i] = true
			default:
				return TerminalEvidenceSummary{}, ErrUncertain
			}
		}
	} else {
		return TerminalEvidenceSummary{}, ErrUnsupported
	}
	if !validTerminalEvidence(summary) {
		return TerminalEvidenceSummary{}, ErrUnsupported
	}
	if err := ctx.Err(); err != nil {
		return TerminalEvidenceSummary{}, err
	}
	w.mu.Lock()
	valid := w.records[id] == e && e.origin == summary.Origin && e.stopped && !e.starting && e.validatingRenewals == 0 && e.responsibilityVersion == version && e.backendInvoked == invoked && e.lease.Version == summary.LeaseVersion && e.lease.Deadline.Equal(summary.LeaseDeadline)
	w.mu.Unlock()
	if !valid {
		return TerminalEvidenceSummary{}, ErrUncertain
	}
	if err := ctx.Err(); err != nil {
		return TerminalEvidenceSummary{}, err
	}
	return summary, nil
}

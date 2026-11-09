package remotetranscode

import "context"

// TerminalState is the closed set of local execution observations. Only
// TerminalRetired attests successful exact encoder AND local input retirement.
// Running/Pending/Unverified/Unknown never authorize reassignment or capacity release.
type TerminalState uint8

const (
	TerminalUnknown TerminalState = iota
	TerminalPending
	TerminalRunning
	TerminalRetired
	TerminalUnverified
)

func (s TerminalState) String() string {
	switch s {
	case TerminalPending:
		return "pending"
	case TerminalRunning:
		return "running"
	case TerminalRetired:
		return "retired"
	case TerminalUnverified:
		return "unverified"
	default:
		return "unknown"
	}
}

// TerminalSnapshot contains no grants, source data, lease extension or execution
// authority. This value is a detached local observation of the complete identity.
// It does not establish source-owner retirement or durable placement retirement.
type TerminalSnapshot struct {
	Identity Identity
	State    TerminalState
	Stopped  bool
}

// Terminal is a HOST-ONLY exact-execution reconciliation boundary. It is not an
// HTTP handler and does not bypass the normal per-operation Security callbacks.
// The trusted host must first authenticate the peer independently and freshly
// authorize the exact old durable placement fence, intended worker boot and
// revoked/expired authority. Never call this based on caller-asserted identity,
// missing membership, time elapsed or worker contact loss.
//
// requestStop seals only this exact execution using its retained owned session
// and inputs. It neither calls Encoder.Stop(publicID) nor starts/renews/resolves
// sources. Status-only absence is unknown. A stop-before-start can attest local
// retirement only after installing the bounded tombstone and highest generation
// floor in this unique worker boot; Forget removes them only once the generation
// is durably terminal, and a forgotten identity then reports retired without a
// new tombstone. Closed/capacity rejection cannot supply that proof. All late
// starts/producers remain charged.
func (w *Worker) Terminal(ctx context.Context, id Identity, requestStop bool) (TerminalSnapshot, error) {
	if ctx == nil || !validIdentity(id) || id.Worker != w.opts.Boot {
		return TerminalSnapshot{}, ErrUnsupported
	}
	if err := ctx.Err(); err != nil {
		return TerminalSnapshot{}, err
	}
	w.mu.Lock()
	known := w.records[id] != nil
	w.mu.Unlock()
	if !known && w.opts.Terminal != nil && w.opts.Terminal(ctx, id) {
		// Forgotten after durable terminal state (Forget): report it retired
		// rather than minting a stop-before-start tombstone, which would claim
		// a generation that may have run never started.
		w.mu.Lock()
		known = w.records[id] != nil
		w.mu.Unlock()
		if !known {
			return TerminalSnapshot{Identity: id, State: TerminalRetired, Stopped: true}, nil
		}
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return TerminalSnapshot{}, err
	}
	e := w.records[id]
	if e == nil {
		if !requestStop || w.closed || len(w.records) >= w.opts.Capacity {
			return TerminalSnapshot{}, ErrUncertain
		}
		e = &execution{id: id, stopped: true, origin: EvidenceStopBeforeStart, negativeBarrierGeneration: id.Generation}
		w.records[id] = e
		key := id.Controller + "/" + id.Session
		if id.Generation > w.highest[key] {
			w.highest[key] = id.Generation
		}
	}
	w.expire(e)
	if requestStop {
		w.stop(e)
	}
	state := TerminalUnknown
	switch result(e) {
	case "pending":
		state = TerminalPending
	case "running":
		state = TerminalRunning
	case "retired":
		state = TerminalRetired
	case "unverified":
		state = TerminalUnverified
	}
	if state == TerminalUnknown {
		return TerminalSnapshot{}, ErrUncertain
	}
	return TerminalSnapshot{Identity: id, State: state, Stopped: e.stopped}, nil
}

// Package wasm is Ether's hosted plugin runtime: it runs plugin code inside
// the Ether process, sandboxed, with no ports and no operating-system
// access. SPEC section 5.2 is the contract.
//
// Three kinds of hosted plugin are supported, all pure Go (CGO_ENABLED=0):
//
//   - JavaScript, on QuickJS-NG compiled to WebAssembly and executed by
//     wazero (via github.com/fastschema/qjs; see THIRD_PARTY_NOTICES.md).
//   - TypeScript, compiled to JavaScript by Ether with esbuild's Go API
//     (see Compile), then run as JavaScript.
//   - Raw WebAssembly modules, executed directly by wazero.
//
// The QuickJS WASI instance has no preopened filesystem, environment or
// arguments. Its std and os modules are disabled. Raw modules cannot import
// WASI. Host capabilities such as HTTP and folders are available only through
// Ether's permission-checked host calls. Studio previews use the same runtime.
//
// # Request ABI (all kinds)
//
// A plugin answers HTTP-shaped JSON requests. The request is
// {"method","path","query","headers","body"} and the response is
// {"status","headers","body"}. In JavaScript the plugin defines
// handle(request) -> response with plain objects (the TypeScript SDK's
// plugin({...}) router produces this); host services are on the ether
// global (ether.kv.get, ether.http.fetch, ether.log, ether.abort, ...).
//
// # Raw WASM ABI
//
// A raw module must export "memory", "alloc" and "handle":
//
//	alloc(size i32) -> i32                     // guest allocator; host writes input here
//	handle(ptr i32, len i32) -> i64            // returns (outPtr<<32)|outLen
//
// Input and output are the request/response JSON above, UTF-8, in linear
// memory. The host copies the output before the next call, so the guest may
// reuse its buffers. Optional hot-path exports:
//
//	rank_key(candPtr, candLen, cfgPtr, cfgLen i32) -> i64
//	cond_match(candPtr, candLen, opPtr, opLen, valPtr, valLen i32) -> i32 (0 or 1)
//
// where candidate/config/value are JSON and operator is a plain string.
// Allowed imports are only "ether.log(ptr,len)",
// "ether.abort(code i32)" (never returns) and
// "ether.host_call(namePtr,nameLen,argsPtr,argsLen,outPtr,outCap i32) -> i32"
// which runs a host service named like "kv.get" with a JSON argument array,
// writes the JSON result into outPtr and returns its length, or a negative
// error code: -1 error, -2 forbidden, -3 governance (unit budget), -4 the
// result did not fit in outCap (the return is then -(needed) if needed > 4,
// see ErrHostResultTooBig handling in raw.go). Any other import (WASI
// included) makes the module refuse to load. On the hot path host_call
// always returns -2: ranking code may only log and abort.
//
// # Governance
//
// Ported from appkit-sandbox semantics. Every call has a wall deadline, a
// memory cap (default 32 MiB, maximum 64 MiB) and a unit budget. Units are
// charged per host call from a cost table plus 1 unit per 100us of wall
// time, checked at host calls and at return. Outcomes are classified as
// ok, error, timeout, forbidden, aborted or governance; traps and timeouts
// poison the instance (it is discarded, never reused) and are counted in
// Stats so the plugin host can apply backoff and disable a failing plugin.
//
// # Hot path
//
// RankBatch and CondMatch run ranking steps and rule conditions in separate
// instances with no host services, a deterministic environment (Date fixed
// at the epoch, Math.random a seeded generator reset per batch), a default
// budget of 2ms per candidate and 50ms per batch, neutral results (rank 0,
// condition false) on exhaustion, and memoization keyed by
// (plugin id, version, config hash, release id).
//
// The budgets bound the plugin's own time. Handing over the facts is host
// work and is not charged. Time is read from the monotonic clock and, when
// that says a budget is spent, from the thread's CPU clock, so time the
// thread spent descheduled on a busy host is not charged either. A batch
// stops starting candidates when its budget is spent; what it finished is
// memoized, so the next batch for the same releases continues from there.
// Separately, a hard deadline of twice the batch budget (Limits.hotKill)
// kills an instance whose code stops returning. The budgets are stated in
// reference-machine time; the engine measures its own speed once per
// process and scales them (see HotScale), so a slow CPU or a race-detector
// build does not cost a light plugin its ranking.
package wasm

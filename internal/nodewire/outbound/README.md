# Outbound operation admission (unwired)

Consumers must provide `RecheckServer(ctx, state)` composed with
`guard.RecheckServer(ctx, state, expectedIdentity, hostname)`. It checks current
roots, expiry and cluster/node/boot policy against the selected TLS connection.
Handshake callbacks remain intact and ordinary Go chain, hostname and EKU checks
run first. A TLS handshake alone does not establish current operation authority.

The initial implementation deliberately uses one fresh TCP/TLS connection per
operation, HTTP/1.1 only, without keepalive, resumption, proxies or redirects.
It builds its own bounded Go HTTP transport rather than retaining caller dial
hooks. The fixed origin is checked before dispatch. Certificate bytes, standard
software keys and roots are copied; opaque signers and callback captures remain
trusted immutable, concurrency-safe dependencies. Policy must be bounded and
cooperate with its admission context. No background polling is installed.

Audited Go 1.27 ordering (`net/http/transport.go`): `getConn` preserves context
values but detaches cancellation with `WithoutCancel`; our RoundTripper carries
the original operation context in a private value and the TLS dial hook restores
it. `dialConn` invokes `customDialTLS` and returns on its error before creating
HTTP read/write loops. Our dial hook completes ordinary TLS verification and the
current policy check before returning the connection. Rejection closes it while
HTTP has no reference to it. `DisableKeepAlives` makes `tryPutIdleConn` reject
pool/handoff, so a canceled dial cannot supply another operation. There is no
`GotConn`-based cancellation barrier. Tests cover actual Guard composition in
both consumers and count raw decrypted bytes on cancellation during admission.

The admission timeout covers TCP/TLS/policy only; response bodies retain normal
request cancellation and streaming backpressure. Point-in-time admission does
not revoke a stream already admitted: source/placement authority must continue
its existing live checks. Snapshot rotation requires a new client/transport;
old transports cannot bypass current Guard policy after roots are removed.

**Unresolved acceptance limitation:** every HEAD, source range read (including
64 KiB reads) and execution RPC pays a TCP/TLS handshake. GPU/production
throughput has not been demonstrated; this is not production-ready throughput
acceptance. Audited reuse would require a separate pre-write admission design.

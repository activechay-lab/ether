# Private source byte transport

This package is an unwired primitive. Trusted composition supplies the fixed HTTPS
endpoint, node certificate identity mapping, current grant authority and atomic
exact-source acquisition. There are no provider URLs, paths or provider headers
on this protocol. Only an opaque source credential goes in the dedicated header.
The endpoint must remain private and must not be registered on the Plex surface.

`Scope` contains the complete placement fence (controller and worker identities
and boot incarnations, session, generation), viewer and immutable revision.
`Authority.Verify` must enforce current placement, viewer policy and revocation;
there is no permissive implementation. HEAD/GET validate before acquisition. Byte
copy revalidates at the configured cadence (maximum one second), including after
reads that straddle that interval. Failed checks stop output. Already authorized
bytes in HTTP/network buffers cannot be recalled. Expiry also bounds the request
context locally. Hosts must keep clocks synchronized; this primitive makes no
clock-skew allowance. A source ignoring context can delay request completion but
cannot release its hold merely because the deadline elapsed.

`Acquire` must atomically enforce current scope and the exact source lifetime's
sealing barrier while acquiring its hold. Verification before acquisition is not
proof the lifetime is still open. It must return the exact captured source object,
matching Scope and a bounded accounting-only Release callback, even on error if a
hold was acquired. Context readers must register any producers that can outlive
the call with the supplied `core.ReadRetirement` before starting them. This
package does not adapt an uncooperative ReaderAt by spawning a goroutine.

The handler admits a bounded registry entry and installs ReadRetirement before
Acquire. Every subsequent exit seals that tracker. Cancelled/disconnected reads
and acquisition errors retain returned holds until all registered producers exit.
Host lifecycle must call `Reconcile` periodically, during drain, and after expiry
when no HTTP requests remain; it calls releases outside registry locks. Pending
entries count against admission and are never evicted by time. The handler owns
no goroutines. No grant registry is duplicated here; injected authority must bound
its own grants, issuance and renewal retention. Configure HTTP server header/time
and connection limits at the private listener too.

Reconcile/Pending report transport hold accounting, not source retirement proof.
A request can still be between verification and admission. Source manager lifetime
sealing/accounting and `core.SourceRetirement` remain final source cleanup proof;
`core.EncoderInputLease` and encoder retirement remain separate. Release never
calls MediaSource.Close. Neither HTTP completion nor network failure attests to
remote producers stopping. Reader.Close/Done only cancel/drain local HTTP work.

The Go client uses verified chain/hostname TLS plus an explicit server identity
binding, presents its client certificate, and binds each grant to its configured
worker boot. No redirects, environment proxies or insecure TLS fallback are used.
HEAD pins size and an ETag derived from complete Scope; each single range checks
status, ETag, total size, exact range, length and EOF. Multipart is rejected.
Single suffix/open-ended ranges and HEAD use standard Go HTTP semantics.
A trusted resolver must guarantee immutable bytes for a revision; cryptographic
content integrity is supplied by TLS, not by hashing the entire media file.

Renew takes a fresh grant from an injected authority, requires every Scope field
unchanged, checks the same HEAD size/ETag and then swaps the underlying bearer.
Existing requests keep their old deadline. The immutable remotetranscode
`Start.Grant` must identify a stable source handle; it must NOT be conflated with
this rotating bearer. Same source handle, revision, object and worker-local input
lease must remain bound throughout renewal. There is no wire renewal endpoint.

Remaining integration belongs to host composition: authority/resolver, source
manager hold/seal adapter, certificate rotation/revocation, lifecycle reconciliation
and worker-local FFmpeg gateway with an EncoderInputLease. FFmpeg cannot inherit
this Go client's mTLS credentials. The existing remotetranscode resolver requires
private literal-IP HTTPS and cannot consume this Reader directly; that resolver
and the later loopback gateway need an explicit trusted integration contract.
No runtime routes, configuration defaults, Plex behavior or UI change here.

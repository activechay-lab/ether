# Jellyfin and Emby controller recovery

Distributed HLS sessions retain their protocol-neutral broker authentication and
playback facts in `clusterplayback_neutral_bindings` (migration 8). The application
supplies `config.Store` as `Runtime.Options.ProtocolSealer`, using the same cluster
credential key as deployment secrets. The entire snapshot is sealed, with session
and execution generation in the authenticated context. There is no plaintext
fallback. Login/API bearer tokens are not collected. Media grants and binding
capabilities exist only inside the ciphertext, never in public session registry
rows, events or logs.

The bounded snapshot contains the existing viewer, device and protocol scope,
media grant, broker/source aliases, stable playback ID, admitted playback options,
selected decision, HLS format/offset/duration, started binding, source metadata,
source revision, and progress/delivery bookkeeping. It excludes actual source
handles, encoder input leases, source ownership, preparation flights, read trackers
and process lifetimes. Restoring metadata cannot recreate any of those owners.

Snapshots expire after ten minutes. Successful media requests refresh them at
most once per thirty seconds; progress writes refresh the current bookkeeping.
The runtime prunes expired ciphertext during its ordinary control tick.
Unhealthy clock observations block restoration, snapshot writes and expiry
cleanup, matching the deployment's continuous clock enforcement. Live
snapshot count is bounded by existing durable session/admission bounds, with a
256 KiB plaintext / 512 KiB ciphertext limit. Stop deletes the ciphertext and
records a non-secret session tombstone. Save and Stop lock the same coordinator
row as takeover before checking that tombstone, preventing an in-flight old save
from recreating stopped credentials on either SQL dialect.

The existing coordinator lease and epoch decide which server can attach to the
healthy worker. Neutral restore does not elect another execution or call encoder
Start. Broker access, session-scoped media and scoped session discovery restore
from the sealed snapshot after cache misses. The normal MediaBrowser middleware
still authenticates the live API credential/device. Broker restore additionally
checks viewer, protocol, device, item and current title/playback policy. Raw media
URLs must match the original media grant. The original public grant and URLs are
preserved; restored StartStream returns the existing matching HLS presentation.
An unavailable coordinator/worker attachment never authorizes local fallback.

After adoption, the existing neutral public registry row moves to the new server
under the coordinator epoch, preserving its active election and report sequence.
The old server cannot serve media, update progress or delete the surviving
binding after it loses that epoch. A read-only source snapshot permits existing
policy and media metadata checks without reopening the source.

All hooks are inactive without an actual distributed Jellyfin/Emby HLS session.
Standalone sessions, other protocols and existing protocol responses keep their
current behavior. Direct-file, live and DASH sessions do not acquire this HLS
restoration contract.

`TestNeutralControllerLossKeepsHealthyEncoder` covers Jellyfin and Emby using real
neutral brokers, a shared SQLite registry, sealed configuration keys, mTLS source
and worker RPC, and the shipping coordinator epoch adoption/Attach path. It checks
an unchanged encoder fence, exactly one encoder Start, unique complete segments
0–7, the unchanged playlist URL, progress on the survivor, registry ownership,
wrong-device/protocol/grant rejection, stale-server rejection, wrong-key and
expired-snapshot rejection, deletion on Stop, and blocked credential resurrection.
Segment bodies are synthetic. This is a controller/session invariant, not proof
of real decoder continuity, PostgreSQL runtime behavior or measured player stalls.

# Retained output and publication barrier

This package is a delivery-controller primitive for encoder-worker loss. It does
not recover a controller or source, start or retire encoders, maintain sessions in
SQL, publish HTTP playlists, or infer client buffering.

`New` requires explicit global and per-session byte, segment and init counts,
plus a session count. `OpenSession` starts at floor -1. `Retain` charges five
times the declared length before reading (retained backing bytes plus conservative
parser workspace); after EOF validation it releases the workspace charge. Limits
cover payload backing bytes and counts, not Go runtime/allocator overhead. No
partial input is committed. Readers must honor cancellation themselves when a
Read can block: the package creates no goroutines and cannot cancel arbitrary
`io.Reader` implementations. Checks occur between reads and before commit.

Retained entries are staged. `Publish` atomically advances an exact state CAS and
makes a contiguous batch available to `Acquire`. Snapshots copy metadata and
facts; leases expose copy-out `ReadAt`, never a writable slice. Published bytes
cannot be deleted, and session close fails while a reader or reservation is
active. Capacity exhaustion refuses new work; it never silently evicts old bytes.
Use fresh session IDs after close. This intentionally bounded first implementation
has no rolling publication-retirement policy.

`FreezeGeneration(id, expected, next State)` freezes ordinary publication and
returns a store-specific single-use token binding session, old/new generation,
old/new seek epochs and exact old/new sequence floors. `PublishTransition` checks that same state,
then checks every staged segment's stored facts under the publication lock.
Old committed keys remain readable throughout. `AbortTransition` unfreezes without
publishing; staged bytes can then be explicitly deleted. Closing a session also
invalidates its token. This package has no authority to stop a worker.

## Evidence boundary

The initial parser supports single-program MPEGTS with one-packet PAT/PMT,
H.264 Annex B, and ADTS AAC. It validates table CRCs, transport continuity,
PES lengths, timestamp markers, and supported elementary framing. Each track
reports raw 33-bit PES PTS/DTS in 90,000 Hz ticks. LastPTS is the maximum
observed PES timestamp, not the final AAC frame inside that PES or its end time. Clock wrap, discontinuity, unsupported
tracks, malformed structure and incomplete PES deny verified facts. Zero-length
video PES is unsupported because packet-aligned truncation is otherwise
ambiguous; recovery CPU output must use `-omit_video_pes_length 0`, and oversized
video PES is likewise unsupported. fMP4 and init bytes can be retained and
ordinarily published but never authorize a recovery transition.

`Facts.Verified` certifies these structural observations only. H.264 independence
means that SPS/PPS precede the first VCL and that first VCL is IDR. It does not
validate every codec syntax element or prove that a decoder accepts the payload.
Config hashes compare the actual observed SPS/PPS or ADTS configuration bytes.
ADTS validation does not decode audio samples. Arbitrary codec corruption can be
undetectable at this structural layer; these are not decoded-media certificates.

A transition requires equal track identities/configurations, independent incoming
tracks, and strictly advancing observed timestamps. It does **not** establish
sample-end duration, gap-free continuity, AV synchronization, a shared title
clock, client receipt, client retention, decoded output, or seamless playback.
Higher-level recovery acceptance must validate those separately. HTTP completion
belongs to delivery accounting and cannot turn these facts into client-buffer
claims. No filenames, sequence numbers or playlist durations supply media facts.

The offline fixture is 0.3 seconds of generated black video and a generated sine
wave, not external media. It was produced with:

```
ffmpeg -f lavfi -i color=c=black:s=64x64:r=10 \
  -f lavfi -i sine=frequency=440:sample_rate=48000 -t 0.3 \
  -c:v libx264 -preset ultrafast -threads 1 -bf 0 -g 3 \
  -x264-params repeat-headers=1:scenecut=0 -c:a aac -b:a 32k \
  -omit_video_pes_length 0 -f mpegts testdata/cpu.ts
```

Tests need neither ffmpeg nor network access.

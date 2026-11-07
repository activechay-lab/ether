# Live acceptance baseline: v0.3.5

This is the reference that every v0.3.6 live-reliability change is measured against. Ether is unmodified v0.3.5 (`990aef0e4`); the harness files were overlaid on a detached checkout. Each row tunes the channel through the Plex native live tune with Apple TV headers, then plays the returned client playlist in AVPlayer while the protocol judge reads the same playlist. The method, scenarios and pass rules are in `cmd/transcodechaos/README.txt` and `cmd/transcodechaos/live_scenarios.go`.

## Reproduce

```sh
git worktree add --detach /tmp/ether-035 990aef0e4
cp cmd/transcodechaos/live_*.go cmd/transcodechaos/main.go /tmp/ether-035/cmd/transcodechaos/
mkdir -p /tmp/ether-035/tools/avprobe && cp tools/avprobe/main.swift /tmp/ether-035/tools/avprobe/
cd /tmp/ether-035
FF=<pinned jellyfin-ffmpeg 8.1.3 directory>   # scripts/fetch-ffmpeg.sh with FFMPEG_OUT outside the repo
go run ./cmd/transcodechaos -live full -live-parallel 4 \
  -ffmpeg $FF/ffmpeg -ffprobe $FF/ffprobe -live-fixture-ffmpeg ffmpeg \
  -report baseline.json -live-markdown baseline.md
```

Run it outside any sandbox on macOS, or AVPlayer fails every item with -9405. The quick subset is `-live quick -live-parallel 6`; its result is `baseline-quick.{json,md}`. To compare a later run with this baseline, add `-live-baseline docs/acceptance/036-live-reliability/baseline.json`.

## What the baseline shows

- **Steady on raw TS:** about 20 s of "Reconnecting..." at every tune; TTFF is 22.5-22.9 s on every raw-TS Steady row. The broadcast startup burst writes a 16 s lead while the provider decoder prepares (4 s). HLS sources start in 2.7-3.6 s.
- **Steady recovery:** any upstream fault costs 15-30 s of the reconnecting screen, and the audio is silent while it shows. That covers RST, EOF, a 458 refusal, an 8 s stall, an HLS 404, 503, slow segment or playlist stall, an AAC->AC3 switch and a resolution change. The channel-level cooldown dominates, and the decoder loops "not available yet" every 33 ms while it waits. A healthy backup gets the channel back in 3.5 s. Corrupt packets, packet loss, keyframe loss and a 10 s GOP are clean in Steady.
- **Steady clock jump:** the video keeps playing, but the audio stays silent for the rest of the run after a +5000 s timestamp jump.
- **Relay copy, every tune:** about 5.5 s freeze right after the first frame. The first client playlist holds one segment, and AVPlayer warns -16832, "restarting 4 s from end of live playlist".
- **Relay copy faults:**
  - A raw-TS stall, refusals (403/458/509) or keyframe loss stop the playlist for 11-13 s, and AVPlayer logs -12888.
  - AAC->AC3 reaches the client without EXT-X-DISCONTINUITY, with or without a PMT version bump, and in relay-transcode too because audio is copied. AVPlayer then freezes for the rest of the run, without reporting an error.
  - A 10 s GOP gives 10 s segments listed as `EXTINF:4`, and AVPlayer never shows a picture.
  - There is no failover to a healthy backup: AVPlayer fails with -11866 after -12888.
- **HLS passthrough:**
  - A provider playlist stall reaches the client as -12888.
  - A 404 segment becomes -12938 and a 4 s freeze.
  - 503 segments are returned as 502. AVPlayer froze for 33 s in run 1; in run 2 it failed with NSURLError -1008 and did not recover.
  - An untagged AAC->AC3 switch fails the item with -12971; the tagged switch plays.
  - A dead primary gives the client a 502 playlist, with no failover.
  - Untagged clock jumps and resolution changes play, as Apple's player tolerates them.
- **Account limit:** reconnects never overlapped, so the provider saw at most 1 concurrent connection in every row.
- **Burst rows:** ts-stall-8s-burst8 and ts-rst-burst8 behave like the rows without burst. v0.3.5 has no reserve to fill from the provider's backlog.

## Repeatability

The full matrix ran twice on this host:
- Run 1: `-live-parallel 6`, peak load average 54, because other workers were busy.
- Run 2: `-live-parallel 4`, peak load average 23. Run 2 is the table below.

53 of the 62 rows that were in both runs agree on verdict, recovery and client errors, with screen time within 3 s and TTFF within 2 s. The 9 that differ are Steady rows plus one passthrough row:
- `hls-rst/steady`: 15.1 s vs 0.0 s screen.
- `hls-segment-404/steady`: 18.5 s vs 0.0 s screen.
- `hls-primary-dies-backup-healthy/steady`: 6.7 s vs 0.1 s screen.
- `hls-playlist-stall-8s/steady`: 30.3 s vs 21.8 s screen.
- `hls-segment-503/steady`, `hls-segment-slow-8s/steady`, `hls-codec-ac3-untagged/steady` and `hls-segment-503/passthrough` differ only in an added -12888 or in screen time.
- `ts-stall-8s/steady`: in run 1 under the heaviest load it did not recover within 60 s.

Short HLS faults land at a different point of Steady's input fetch cycle on each run, so judge those rows over repeated runs, not once. The four burst rows were added after run 2 and ran once, at load average 13.

Limits that hold for every row:
- AVPlayer on macOS 26 stands in for the Plex tvOS app; the Apple TV and Fire TV device rounds remain.
- Audio is judged from the published segments, not in-player.
- The Jellyfin path is not driven here (see the README).
- TTFF and freeze timings come from a shared host; use `-live-parallel 1` for timing-critical comparisons.

## Results (run 2)
- Commit `990aef0e4`, subset `full`, preset `balanced`, client `plex-appletv`, judge `avfoundation`
- Ether's ffmpeg: ffmpeg version 8.1.3-Jellyfin Copyright (c) 2000-2026 the FFmpeg developers
- Host: darwin/arm64, 2026-11-07 18:58 UTC, 980 s wall clock

**6 of 66 rows pass.** Peak host load average during the run: 23.

| Row | Ether path | Verdict | TTFF s | Screen s (freeze+slate) | Silence s | Longest no-new-segment s | Client errors | Recovered |
|---|---|---|---:|---:|---:|---:|---|---|
| `healthy-ts/relay-copy` | copy | FAIL | 3.3 | 5.5 (5.5+0.0) | 0.0 | 4.3 |  | true |
| `healthy-ts/relay-transcode` | transcode | FAIL | 4.3 | 5.1 (5.1+0.0) | 0.0 | 4.4 |  | true |
| `healthy-ts/steady` | broadcast | FAIL | 22.8 | 0.0 (0.0+0.0) | 0.6 | 4.0 |  | true |
| `healthy-hls/passthrough` | hls_passthrough | PASS | 0.2 | 0.0 (0.0+0.0) | 0.0 | 4.2 |  | true |
| `healthy-hls/relay-transcode` | transcode | FAIL | 0.2 | 4.0 (4.0+0.0) | 0.0 | 4.5 |  | true |
| `healthy-hls/steady` | broadcast | FAIL | 2.8 | 0.0 (0.0+0.0) | 1.1 | 4.0 |  | true |
| `hls-playlist-stall-8s/passthrough` | hls_passthrough | FAIL | 0.1 | 1.1 (1.1+0.0) | 0.0 | 11.0 | errorlog -12888 @18s | true |
| `hls-playlist-stall-8s/relay-transcode` | transcode | FAIL | 0.2 | 5.0 (5.0+0.0) | 0.0 | 12.5 | errorlog -12888 @22s | true |
| `hls-playlist-stall-8s/steady` | broadcast | FAIL | 2.7 | 21.8 (0.0+21.8) | 23.8 | 8.5 | errorlog -12888 @20s | true |
| `ts-stall-8s/relay-copy` | copy | FAIL | 3.8 | 15.6 (15.6+0.0) | 0.0 | 11.0 | errorlog -12888 @18s | true |
| `ts-stall-8s/relay-transcode` | transcode | FAIL | 4.2 | 12.9 (12.9+0.0) | 0.0 | 10.5 | errorlog -12888 @18s | true |
| `ts-stall-8s/steady` | broadcast | FAIL | 22.9 | 19.7 (0.0+19.7) | 21.9 | 4.0 |  | true |
| `hls-segment-404/passthrough` | hls_passthrough | FAIL | 0.2 | 4.0 (4.0+0.0) | 0.0 | 4.1 | errorlog -12938 @16s<br>http segment 404 x1 | true |
| `hls-segment-404/steady` | broadcast | FAIL | 3.0 | 0.0 (0.0+0.0) | 5.3 | 8.0 | errorlog -12888 @20s | true |
| `hls-segment-503/passthrough` | hls_passthrough | FAIL | 0.2 | 36.0 (36.0+0.0) | 0.0 | 4.0 | errorlog -16848 @16s<br>failed NSURLErrorDomain -1008<br>http segment 502 x2 | false |
| `hls-segment-503/steady` | broadcast | FAIL | 2.7 | 21.8 (0.0+21.8) | 23.8 | 8.5 | errorlog -12888 @20s | true |
| `hls-segment-slow-8s/passthrough` | hls_passthrough | PASS | 0.2 | 0.0 (0.0+0.0) | 0.0 | 4.0 |  | true |
| `hls-segment-slow-8s/steady` | broadcast | FAIL | 2.8 | 21.7 (0.0+21.7) | 24.1 | 8.0 | errorlog -12888 @20s | true |
| `hls-rst/passthrough` | hls_passthrough | FAIL | 0.2 | 0.0 (0.0+0.0) | 0.0 | 4.1 | errorlog -16848 @16s<br>http segment 502 x1 | true |
| `hls-rst/steady` | broadcast | FAIL | 3.1 | 0.0 (0.0+0.0) | 1.3 | 6.0 |  | true |
| `ts-rst/relay-copy` | copy | FAIL | 3.8 | 5.5 (5.5+0.0) | 0.0 | 4.3 |  | true |
| `ts-rst/relay-transcode` | transcode | FAIL | 4.5 | 4.9 (4.9+0.0) | 0.0 | 4.2 |  | true |
| `ts-rst/steady` | broadcast | FAIL | 22.9 | 15.6 (0.0+15.6) | 17.8 | 4.0 |  | true |
| `ts-eof/relay-copy` | copy | FAIL | 3.9 | 7.4 (7.4+0.0) | 0.0 | 4.3 |  | true |
| `ts-eof/steady` | broadcast | FAIL | 22.9 | 15.6 (0.0+15.6) | 17.8 | 4.0 |  | true |
| `ts-refuse-458/relay-copy` | copy | FAIL | 3.9 | 15.6 (15.6+0.0) | 0.0 | 13.0 | errorlog -12888 @22s | true |
| `ts-refuse-458/steady` | broadcast | FAIL | 22.9 | 15.6 (0.0+15.6) | 17.8 | 4.0 |  | true |
| `ts-refuse-403/relay-copy` | copy | FAIL | 3.9 | 15.5 (15.5+0.0) | 0.0 | 13.0 | errorlog -12888 @22s | true |
| `ts-refuse-509/relay-copy` | copy | FAIL | 3.9 | 15.6 (15.6+0.0) | 0.0 | 13.0 | errorlog -12888 @22s | true |
| `ts-conn-limit-1/relay-copy` | copy | FAIL | 3.9 | 7.4 (7.4+0.0) | 0.0 | 4.3 |  | true |
| `ts-conn-limit-1/steady` | broadcast | FAIL | 22.9 | 15.6 (0.0+15.6) | 17.8 | 4.0 |  | true |
| `primary-dies-backup-healthy/relay-copy` | copy | FAIL | 3.8 | 42.5 (42.5+0.0) | 0.0 | 46.1 | errorlog -12888 @22s<br>failed AVFoundationErrorDomain -11866 | false |
| `primary-dies-backup-healthy/steady` | broadcast | FAIL | 22.9 | 3.8 (0.0+3.8) | 5.9 | 4.0 |  | true |
| `hls-primary-dies-backup-healthy/passthrough` | hls_passthrough | FAIL | 0.2 | 36.0 (36.0+0.0) | 0.0 | 48.0 | errorlog -16848 @16s<br>failed NSURLErrorDomain -1008<br>http playlist 502 x90 | false |
| `hls-primary-dies-backup-healthy/steady` | broadcast | PASS | 3.3 | 0.1 (0.0+0.1) | 1.5 | 4.0 |  | true |
| `primary-dies-backup-absent/relay-copy` | copy | FAIL | 3.8 | 42.4 (42.4+0.0) | 0.0 | 46.1 | errorlog -12888 @22s<br>failed AVFoundationErrorDomain -11866 | false |
| `primary-dies-backup-absent/steady` | broadcast | FAIL | 22.9 | 20.8 (0.0+20.8) | 37.0 | 4.0 |  | false |
| `primary-dies-backup-failing/steady` | broadcast | FAIL | 22.9 | 20.8 (0.0+20.8) | 37.0 | 4.0 |  | false |
| `ts-codec-ac3-pmt/relay-copy` | copy | FAIL | 3.9 | 41.4 (41.4+0.0) | 0.0 | 4.3 | unmarked codec change between segments (Apple players fail or freeze) | false |
| `ts-codec-ac3-pmt/relay-transcode` | transcode | FAIL | 4.4 | 40.8 (40.8+0.0) | 0.0 | 4.2 | unmarked codec change between segments (Apple players fail or freeze) | false |
| `ts-codec-ac3-pmt/steady` | broadcast | FAIL | 22.9 | 18.5 (0.0+18.5) | 20.7 | 4.0 |  | true |
| `ts-codec-ac3-nobump/relay-copy` | copy | FAIL | 3.8 | 41.4 (41.4+0.0) | 0.0 | 4.3 | unmarked codec change between segments (Apple players fail or freeze) | false |
| `ts-codec-ac3-nobump/steady` | broadcast | FAIL | 22.9 | 18.5 (0.0+18.5) | 20.6 | 4.0 |  | true |
| `hls-codec-ac3-untagged/passthrough` | hls_passthrough | FAIL | 0.2 | 43.9 (43.9+0.0) | 0.0 | 4.0 | failed CoreMediaErrorDomain -12971<br>unmarked codec change between segments (Apple players fail or freeze) | false |
| `hls-codec-ac3-untagged/steady` | broadcast | FAIL | 2.8 | 21.5 (0.0+21.5) | 23.6 | 8.0 | errorlog -12888 @20s | true |
| `hls-codec-ac3-tagged/passthrough` | hls_passthrough | PASS | 0.2 | 0.0 (0.0+0.0) | 0.0 | 4.0 |  | true |
| `ts-resolution/relay-copy` | copy | FAIL | 3.8 | 5.5 (5.5+0.0) | 0.0 | 4.3 |  | true |
| `ts-resolution/steady` | broadcast | FAIL | 22.9 | 18.6 (0.0+18.6) | 20.7 | 4.0 |  | true |
| `hls-resolution-untagged/passthrough` | hls_passthrough | PASS | 0.2 | 0.0 (0.0+0.0) | 0.0 | 4.0 |  | true |
| `ts-clock-jump/relay-copy` | copy | FAIL | 3.8 | 5.5 (5.5+0.0) | 0.0 | 4.3 |  | true |
| `ts-clock-jump/steady` | broadcast | FAIL | 22.9 | 0.0 (0.0+0.0) | 36.0 | 4.0 |  | true |
| `ts-clock-reset/relay-copy` | copy | FAIL | 3.9 | 5.5 (5.5+0.0) | 0.0 | 4.3 |  | true |
| `ts-clock-reset/steady` | broadcast | FAIL | 22.9 | 19.7 (0.0+19.7) | 31.9 | 4.0 |  | false |
| `hls-clock-jump-untagged/passthrough` | hls_passthrough | PASS | 0.2 | 0.0 (0.0+0.0) | 0.0 | 4.1 |  | true |
| `ts-long-gop-10s/relay-copy` | copy | FAIL | none | 0.0 (0.0+0.0) | 0.0 | 10.5 | player never showed the channel | false |
| `ts-long-gop-10s/steady` | broadcast | FAIL | 22.9 | 0.0 (0.0+0.0) | 0.7 | 4.0 |  | true |
| `ts-keyframe-loss/relay-copy` | copy | FAIL | 3.9 | 10.5 (10.5+0.0) | 0.0 | 12.0 | errorlog -12888 @18s | true |
| `ts-keyframe-loss/steady` | broadcast | FAIL | 22.9 | 0.0 (0.0+0.0) | 0.7 | 4.0 |  | true |
| `ts-corrupt/relay-copy` | copy | FAIL | 3.8 | 13.1 (13.1+0.0) | 0.0 | 4.3 |  | true |
| `ts-corrupt/steady` | broadcast | FAIL | 22.9 | 0.0 (0.0+0.0) | 0.7 | 4.0 |  | true |
| `ts-packet-loss/relay-copy` | copy | FAIL | 3.8 | 13.5 (13.5+0.0) | 0.0 | 4.3 |  | true |
| `ts-packet-loss/steady` | broadcast | FAIL | 22.9 | 0.0 (0.0+0.0) | 0.7 | 4.0 |  | true |
| `ts-stall-8s-burst8/relay-copy` | copy | FAIL | 3.8 | 15.6 (15.6+0.0) | 0.0 | 11.0 | errorlog -12888 @18s | true |
| `ts-stall-8s-burst8/steady` | broadcast | FAIL | 22.9 | 15.1 (0.0+15.1) | 17.2 | 4.0 |  | true |
| `ts-rst-burst8/relay-copy` | copy | FAIL | 3.8 | 7.3 (7.3+0.0) | 0.0 | 4.3 |  | true |
| `ts-rst-burst8/steady` | broadcast | FAIL | 22.5 | 13.6 (0.0+13.6) | 15.4 | 4.0 |  | true |

## Failure reasons

- `healthy-ts/relay-copy` (Healthy raw TS control): screen 5.5s > 0.0s
- `healthy-ts/relay-transcode` (Healthy raw TS control): screen 5.1s > 0.0s
- `healthy-ts/steady` (Healthy raw TS control): silence 0.6s > 0.0s; ttff 22.8s > 5s
- `healthy-hls/relay-transcode` (Healthy provider HLS control): screen 4.0s > 0.0s
- `healthy-hls/steady` (Healthy provider HLS control): silence 1.1s > 0.0s
- `hls-playlist-stall-8s/passthrough` (Provider playlist stops advancing for 8 s, then catches up): client errors: errorlog -12888 @18s; screen 1.1s > 0.0s; no new segment for 11.0s (> 1.5x TD 4s)
- `hls-playlist-stall-8s/relay-transcode` (Provider playlist stops advancing for 8 s, then catches up): client errors: errorlog -12888 @22s; screen 5.0s > 0.0s; no new segment for 12.5s (> 1.5x TD 4s)
- `hls-playlist-stall-8s/steady` (Provider playlist stops advancing for 8 s, then catches up): client errors: errorlog -12888 @20s; screen 21.8s > 0.0s; silence 23.8s > 0.0s; no new segment for 8.5s (> 1.5x TD 4s)
- `ts-stall-8s/relay-copy` (Raw TS body sends nothing for 8 s on an open connection): client errors: errorlog -12888 @18s; screen 15.6s > 0.0s; no new segment for 11.0s (> 1.5x TD 4s)
- `ts-stall-8s/relay-transcode` (Raw TS body sends nothing for 8 s on an open connection): client errors: errorlog -12888 @18s; screen 12.9s > 0.0s; no new segment for 10.5s (> 1.5x TD 4s)
- `ts-stall-8s/steady` (Raw TS body sends nothing for 8 s on an open connection): screen 19.7s > 0.0s; silence 21.9s > 0.0s; ttff 22.9s > 5s
- `hls-segment-404/passthrough` (One provider segment returns 404): client errors: errorlog -12938 @16s, http segment 404 x1; screen 4.0s > 0.0s
- `hls-segment-404/steady` (One provider segment returns 404): client errors: errorlog -12888 @20s; silence 5.3s > 0.0s; no new segment for 8.0s (> 1.5x TD 4s)
- `hls-segment-503/passthrough` (Two provider segments return 503): client errors: errorlog -16848 @16s, failed NSURLErrorDomain -1008, http segment 502 x2; did not recover; screen 36.0s > 0.0s
- `hls-segment-503/steady` (Two provider segments return 503): client errors: errorlog -12888 @20s; screen 21.8s > 0.0s; silence 23.8s > 0.0s; no new segment for 8.5s (> 1.5x TD 4s)
- `hls-segment-slow-8s/steady` (Provider segment responses wait 8 s): client errors: errorlog -12888 @20s; screen 21.7s > 0.0s; silence 24.1s > 0.0s; no new segment for 8.0s (> 1.5x TD 4s)
- `hls-rst/passthrough` (Provider resets HLS requests for 3 s): client errors: errorlog -16848 @16s, http segment 502 x1
- `hls-rst/steady` (Provider resets HLS requests for 3 s): silence 1.3s > 0.0s; no new segment for 6.0s (> 1.5x TD 4s)
- `ts-rst/relay-copy` (Raw TS connection reset mid-stream): screen 5.5s > 0.0s
- `ts-rst/relay-transcode` (Raw TS connection reset mid-stream): screen 4.9s > 0.0s
- `ts-rst/steady` (Raw TS connection reset mid-stream): screen 15.6s > 0.0s; silence 17.8s > 0.0s; ttff 22.9s > 5s
- `ts-eof/relay-copy` (Raw TS body ends cleanly mid-stream): screen 7.4s > 0.0s
- `ts-eof/steady` (Raw TS body ends cleanly mid-stream): screen 15.6s > 0.0s; silence 17.8s > 0.0s; ttff 22.9s > 5s
- `ts-refuse-458/relay-copy` (Reset, then reconnects refused 458 for 10 s): client errors: errorlog -12888 @22s; screen 15.6s > 0.0s; no new segment for 13.0s (> 1.5x TD 4s)
- `ts-refuse-458/steady` (Reset, then reconnects refused 458 for 10 s): screen 15.6s > 0.0s; silence 17.8s > 0.0s; ttff 22.9s > 5s
- `ts-refuse-403/relay-copy` (Reset, then reconnects refused 403 for 10 s): client errors: errorlog -12888 @22s; screen 15.5s > 0.0s; no new segment for 13.0s (> 1.5x TD 4s)
- `ts-refuse-509/relay-copy` (Reset, then reconnects refused 509 for 10 s): client errors: errorlog -12888 @22s; screen 15.6s > 0.0s; no new segment for 13.0s (> 1.5x TD 4s)
- `ts-conn-limit-1/relay-copy` (Account limit 1; reset mid-stream; reconnect must not overlap): screen 7.4s > 0.0s
- `ts-conn-limit-1/steady` (Account limit 1; reset mid-stream; reconnect must not overlap): screen 15.6s > 0.0s; silence 17.8s > 0.0s; ttff 22.9s > 5s
- `primary-dies-backup-healthy/relay-copy` (Primary resets and stays down; healthy backup): client errors: errorlog -12888 @22s, failed AVFoundationErrorDomain -11866; did not recover; screen 42.5s > 3.0s; no new segment for 46.1s (> 1.5x TD 4s)
- `primary-dies-backup-healthy/steady` (Primary resets and stays down; healthy backup): screen 3.8s > 3.0s; silence 5.9s > 3.0s; ttff 22.9s > 5s
- `hls-primary-dies-backup-healthy/passthrough` (HLS primary down for good; healthy HLS backup): client errors: errorlog -16848 @16s, failed NSURLErrorDomain -1008, http playlist 502 x90; did not recover; screen 36.0s > 3.0s; no new segment for 48.0s (> 1.5x TD 4s)
- `primary-dies-backup-absent/relay-copy` (Primary down for good; backup URL refuses connections): client errors: errorlog -12888 @22s, failed AVFoundationErrorDomain -11866; no new segment for 46.1s (> 1.5x TD 4s)
- `primary-dies-backup-absent/steady` (Primary down for good; backup URL refuses connections): ttff 22.9s > 5s
- `primary-dies-backup-failing/steady` (Primary and backup both down for good): ttff 22.9s > 5s
- `ts-codec-ac3-pmt/relay-copy` (Audio AAC->AC3, same PID, PMT version bumped): client errors: unmarked codec change between segments (Apple players fail or freeze); did not recover; screen 41.4s > 1.0s
- `ts-codec-ac3-pmt/relay-transcode` (Audio AAC->AC3, same PID, PMT version bumped): client errors: unmarked codec change between segments (Apple players fail or freeze); did not recover; screen 40.8s > 1.0s
- `ts-codec-ac3-pmt/steady` (Audio AAC->AC3, same PID, PMT version bumped): screen 18.5s > 1.0s; silence 20.7s > 1.0s; ttff 22.9s > 5s
- `ts-codec-ac3-nobump/relay-copy` (Audio AAC->AC3, same PID, no PMT version bump): client errors: unmarked codec change between segments (Apple players fail or freeze); did not recover; screen 41.4s > 1.0s
- `ts-codec-ac3-nobump/steady` (Audio AAC->AC3, same PID, no PMT version bump): screen 18.5s > 1.0s; silence 20.6s > 1.0s; ttff 22.9s > 5s
- `hls-codec-ac3-untagged/passthrough` (Provider HLS switches AAC->AC3 without EXT-X-DISCONTINUITY): client errors: failed CoreMediaErrorDomain -12971, unmarked codec change between segments (Apple players fail or freeze); did not recover; screen 43.9s > 1.0s
- `hls-codec-ac3-untagged/steady` (Provider HLS switches AAC->AC3 without EXT-X-DISCONTINUITY): client errors: errorlog -12888 @20s; screen 21.5s > 1.0s; silence 23.6s > 1.0s; no new segment for 8.0s (> 1.5x TD 4s)
- `ts-resolution/relay-copy` (Video 640x360 -> 1280x720 mid-stream): screen 5.5s > 1.0s
- `ts-resolution/steady` (Video 640x360 -> 1280x720 mid-stream): screen 18.6s > 1.0s; silence 20.7s > 1.0s; ttff 22.9s > 5s
- `ts-clock-jump/relay-copy` (Timestamps jump +5000 s): screen 5.5s > 1.0s
- `ts-clock-jump/steady` (Timestamps jump +5000 s): silence 36.0s > 1.0s; ttff 22.9s > 5s
- `ts-clock-reset/relay-copy` (Timestamps restart near zero): screen 5.5s > 1.0s
- `ts-clock-reset/steady` (Timestamps restart near zero): did not recover; screen 19.7s > 1.0s; silence 31.9s > 1.0s; ttff 22.9s > 5s
- `ts-long-gop-10s/relay-copy` (10 s GOP raw TS): client errors: player never showed the channel; did not recover; no new segment for 10.5s (> 1.5x TD 4s); 6 segments exceed TD+0.5s (longest EXTINF 4.0s, media 10.0s); no provider picture
- `ts-long-gop-10s/steady` (10 s GOP raw TS): silence 0.7s > 0.0s; ttff 22.9s > 5s
- `ts-keyframe-loss/relay-copy` (Keyframes lost for 8 s): client errors: errorlog -12888 @18s; screen 10.5s > 2.0s; no new segment for 12.0s (> 1.5x TD 4s); 1 segments exceed TD+0.5s (longest EXTINF 4.0s, media 12.0s)
- `ts-keyframe-loss/steady` (Keyframes lost for 8 s): ttff 22.9s > 5s
- `ts-corrupt/relay-copy` (1% of packets carry garbage for 8 s): screen 13.1s > 2.0s
- `ts-corrupt/steady` (1% of packets carry garbage for 8 s): ttff 22.9s > 5s
- `ts-packet-loss/relay-copy` (2% packet loss for 8 s): screen 13.5s > 2.0s
- `ts-packet-loss/steady` (2% packet loss for 8 s): ttff 22.9s > 5s
- `ts-stall-8s-burst8/relay-copy` (8 s raw TS stall; the provider sends 8 s of backlog on every connect): client errors: errorlog -12888 @18s; screen 15.6s > 0.0s; no new segment for 11.0s (> 1.5x TD 4s)
- `ts-stall-8s-burst8/steady` (8 s raw TS stall; the provider sends 8 s of backlog on every connect): screen 15.1s > 0.0s; silence 17.2s > 0.0s; ttff 22.9s > 5s
- `ts-rst-burst8/relay-copy` (Raw TS reset; the provider sends 8 s of backlog on every connect): screen 7.3s > 0.0s
- `ts-rst-burst8/steady` (Raw TS reset; the provider sends 8 s of backlog on every connect): screen 13.6s > 0.0s; silence 15.4s > 0.0s; ttff 22.5s > 5s

## How each number is measured

- **Screen** = freeze + slate seconds after the first provider picture. Budgets per preset are in `cmd/transcodechaos/live_scenarios.go`.
- **Freeze**: with the AVFoundation judge, gaps of at least 0.5 s between frames AVPlayer delivered plus seconds of repeated (held) provider frames. Without it, a player buffer model over observed segment arrivals plus held provider frames in the published media (ffmpeg freezedetect).
- **Slate**: the fixture carries a green band on top; frames without it are an Ether screen or black. Counted from AVPlayer's frames (or 10 Hz samples of the published segments without the player).
- Everything before the first provider picture is startup and is judged by **TTFF** (tune to first provider picture); an Ether card shown at tune counts against TTFF, not screen.
- **Silence**: ffmpeg silencedetect (-45 dB, 0.25 s) on the published segments. In-player audio is not measured: AVFoundation audio taps do not attach to HLS items.
- **Longest no-new-segment**: the protocol judge polls the client playlist every 0.5 s. Apple players stop with -12888 after 1.5x target duration without a new segment; ExoPlayer (Fire TV) after 3.5x.
- **Client errors**: AVPlayer item failures and error-log codes, HTTP errors Ether returned to the client, and unmarked codec changes between consecutive segments (Apple -12971).
- **TTFF**: tune request to the first frame AVPlayer delivered (or the first downloaded segment without the player).

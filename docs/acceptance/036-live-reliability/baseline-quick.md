# Live acceptance: v0.3.5 baseline (990aef0e4), quick subset

- Commit `990aef0e4`, subset `quick`, preset `balanced`, judge `avfoundation`
- Ether's ffmpeg: ffmpeg version 8.1.3-Jellyfin Copyright (c) 2000-2026 the FFmpeg developers
- Host: darwin/arm64, 2026-11-07 18:43 UTC, 73 s wall clock

**0 of 6 rows pass.**

| Row | Ether path | Verdict | TTFF s | Screen s (freeze+slate) | Silence s | Longest no-new-segment s | Client errors | Recovered |
|---|---|---|---:|---:|---:|---:|---|---|
| `hls-playlist-stall-8s/passthrough` | hls_passthrough | FAIL | 0.2 | 1.1 (1.1+0.0) | 0.0 | 11.0 | errorlog -12888 @18s | true |
| `ts-stall-8s/steady` | broadcast | FAIL | 22.5 | 19.7 (0.0+19.7) | 21.5 | 4.0 |  | true |
| `ts-rst/relay-copy` | copy | FAIL | 3.9 | 7.3 (7.3+0.0) | 0.0 | 4.3 |  | true |
| `primary-dies-backup-healthy/steady` | broadcast | FAIL | 22.9 | 3.6 (0.0+3.6) | 5.7 | 4.0 |  | true |
| `ts-codec-ac3-pmt/relay-copy` | copy | FAIL | 3.9 | 41.3 (41.3+0.0) | 0.0 | 4.3 | unmarked codec change between segments (Apple players fail or freeze) | false |
| `ts-long-gop-10s/relay-copy` | copy | FAIL | none | 0.0 (0.0+0.0) | 0.0 | 10.5 | player never showed the channel | false |

## Failure reasons

- `hls-playlist-stall-8s/passthrough` (Provider playlist stops advancing for 8 s, then catches up): client errors: errorlog -12888 @18s; screen 1.1s > 0.0s; no new segment for 11.0s (> 1.5x TD 4s)
- `ts-stall-8s/steady` (Raw TS body sends nothing for 8 s on an open connection): screen 19.7s > 0.0s; silence 21.5s > 0.0s; ttff 22.5s > 5s
- `ts-rst/relay-copy` (Raw TS connection reset mid-stream): screen 7.3s > 0.0s
- `primary-dies-backup-healthy/steady` (Primary resets and stays down; healthy backup): screen 3.6s > 3.0s; silence 5.7s > 3.0s; ttff 22.9s > 5s
- `ts-codec-ac3-pmt/relay-copy` (Audio AAC->AC3, same PID, PMT version bumped): client errors: unmarked codec change between segments (Apple players fail or freeze); did not recover; screen 41.3s > 1.0s
- `ts-long-gop-10s/relay-copy` (10 s GOP raw TS): client errors: player never showed the channel; did not recover; no new segment for 10.5s (> 1.5x TD 4s); 6 segments exceed TD+0.5s (longest EXTINF 4.0s, media 10.0s); no provider picture

## How each number is measured

- **Screen** = freeze + slate seconds after the first provider picture. Budgets per preset are in `cmd/transcodechaos/live_scenarios.go`.
- **Freeze**: with the AVFoundation judge, gaps of at least 0.5 s between frames AVPlayer delivered plus seconds of repeated (held) provider frames. Without it, a player buffer model over observed segment arrivals plus held provider frames in the published media (ffmpeg freezedetect).
- **Slate**: the fixture carries a green band on top; frames without it are an Ether screen or black. Counted from AVPlayer's frames (or 10 Hz samples of the published segments without the player).
- Everything before the first provider picture is startup and is judged by **TTFF** (tune to first provider picture); an Ether card shown at tune counts against TTFF, not screen.
- **Silence**: ffmpeg silencedetect (-45 dB, 0.25 s) on the published segments. In-player audio is not measured: AVFoundation audio taps do not attach to HLS items.
- **Longest no-new-segment**: the protocol judge polls the client playlist every 0.5 s. Apple players stop with -12888 after 1.5x target duration without a new segment.
- **Client errors**: AVPlayer item failures and error-log codes, HTTP errors Ether returned to the client, and unmarked codec changes between consecutive segments (Apple -12971).
- **TTFF**: tune request to the first frame AVPlayer delivered (or the first downloaded segment without the player).

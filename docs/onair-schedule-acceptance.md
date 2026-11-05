# On Air schedule acceptance (P1)

This slice follows the 2026-11-05 continuous-output ruling at spec revision
2129fdbad. It contains no clip-track, track-budget, instant-ident or transcode
changes. The previous off-air playout implementation remains unchanged for
P2–P4 to replace. No UI changes.

## Delivered

- Additive `core.ChannelGraphics`, `ClipRequest` and `ClipRef`, exactly as §4.2.
  There are no HLSRequest or config additions and no PersonalPicker.
- Canonical ChannelLook JSON schema, including placement and the fields reserved
  for reels, inside breaks and plugin segments. Optional Options.Graphics.
- A private duration lookup on the scheduling channel copy. This preserves the
  Schedule API and performs no cache access, rendering or I/O. The service
  supplies Graphics.Duration. Without it, graphics paths are disabled.
- Gap placement consumes existing alignment/repeat gaps before filler/slate;
  insert placement adds clip slots. Only positive multiples of four seconds
  from the template duration lookup become schedule entries. Tune idents are
  not scheduled. Between-programme breaks produce break/back; up-next is
  followed by tonight after 18:00 local time. Inside breaks and reel selection
  remain reserved for their later slices.
- Guide coalescing for Guide, XMLTV and the Plex grid, all using the existing
  Guide boundary. Insert mode extends the preceding row. Gap mode uses the
  original calendar exactly, including its filler and visible off-air rows.
  Playout's programmes method keeps the uncoalesced schedule.
- Epoch calendar anchors survive adding a gap look. LegacyCalendar rejects
  looked channels, and weeklySchedule delegates to the same calendar anchors
  when the original scheduling fields require them.
- Background Ensure calls for moments in the next three hours on enabled
  channels watched in the last two hours. Bindings are channel and upcoming
  programme values; ReelID and personal viewers remain unused.

## Lasting tests and verification

The no-look weekly golden was computed with the original scheduler before
editing it. No old scheduler copy remains. SHA-256:
`732264dddf663f4019748958389d04ef1be478b581e737b48874fd9ca901ea6d`.

Tests cover overlapping gap/insert windows, gap guide equality, unchanged real
programme IDs, and epoch calendar stability. They use fake ChannelGraphics
and do not invoke ffmpeg.

Gates:

- `nice -n 10 go test -count=1 -p 2 ./internal/livetv ./internal/core`
- `nice -n 10 go vet ./internal/livetv ./internal/core`
- `nice -n 10 go build ./...`
- `CGO_ENABLED=0 nice -n 10 go build -o /tmp/ether-p1 ./cmd/ether`
- `git diff --check`

The transient, now-removed W4 clip-track implementation was also tested, but
those results and its ident latency measurements do not describe P1.

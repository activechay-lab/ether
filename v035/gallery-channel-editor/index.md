# Channel programming editor

Mock console at `127.0.0.1:32622/ether/livetv?channel=v-80s`. Desktop: 1440 × 1100. Phone: 390 × 844. Each view is captured in dark and light. Ether ships none of the programme or commercial content represented by these mock fixtures.

| View | Desktop dark | Desktop light | Phone dark | Phone light |
| --- | --- | --- | --- | --- |
| Programming | [View](programming-desktop-dark.png) | [View](programming-desktop-light.png) | [View](programming-phone-dark.png) | [View](programming-phone-light.png) |
| Block drawer | [View](block-desktop-dark.png) | [View](block-desktop-light.png) | [View](block-phone-dark.png) | [View](block-phone-light.png) |
| Recurring pin drawer | [View](pin-desktop-dark.png) | [View](pin-desktop-light.png) | [View](pin-phone-dark.png) | [View](pin-phone-light.png) |
| Dated special drawer | [View](special-desktop-dark.png) | [View](special-desktop-light.png) | [View](special-phone-dark.png) | [View](special-phone-light.png) |
| Breaks | [View](breaks-desktop-dark.png) | [View](breaks-desktop-light.png) | [View](breaks-phone-dark.png) | [View](breaks-phone-light.png) |
| Creative elements | [View](creative-desktop-dark.png) | [View](creative-desktop-light.png) | [View](creative-phone-dark.png) | [View](creative-phone-light.png) |
| Timeline | [View](timeline-desktop-dark.png) | [View](timeline-desktop-light.png) | [View](timeline-phone-dark.png) | [View](timeline-phone-light.png) |
| Schedule item drawer | [View](item-desktop-dark.png) | [View](item-desktop-light.png) | [View](item-phone-dark.png) | [View](item-phone-light.png) |

## Persona findings

All three personas reprogrammed the existing mock 80s Action channel through the console: morning daypart from a collection, prime-time movie block, recurring weekday 20:00–20:30 sitcom strip, natural 1980s commercial pods from the Saturday morning ads reel, station IDs, tune-in ident and up-next. The fake 1985 show The Crimson Harbor substitutes for Cheers in the catalogue picker. NEON 84 provides the graphics; the channel controls their scheduling.

- **Non-technical owner:** named blocks and a dedicated Add menu replace the conflicting channel-wide source and marathon choices. Blocks, pins and specials open stacked drawers. The initial build saved successfully.
- **Power user:** repeated the complete build after loading final code, including a weekday strip starting at season 1, episode 1, and natural commercial breaks. Per-source episode order and weights, per-block repeat windows and timing rules, channel breaks and inherited block overrides are available in the same workflow.
- **Accessibility user:** independently repeated the complete build with reduced motion and keyboard activation: Enter opens drawers, Arrow/Enter selects menu choices, Space toggles switches, and the labelled title search selects the show. [Saved keyboard build](persona-accessibility.png). Native AppKit menu highlighting must be moved with Arrow keys before Enter; focusing an arbitrary option does not change its highlighted selection.

Corrections made during verification: removed the duplicate inside-programmes switch so Break placement is the sole placement rule; strip slots hide the overrun control; blank channel timing remains an explicit Default; timeline Add opens a pin drawer rather than silently inserting an empty pin; source start-point changes reset strip continuity anchors; direct channel asset selectors configure idents, promos and bumpers without editing the shared style. Nested drawers fit the phone and desktop viewport after their entrance animation settles.

The timeline uses the snapshot endpoint. Its mock fixture deliberately includes programmes, nested commercial spots, station IDs, plugin segments, specials and off-air gaps to exercise all visual kinds; it is illustrative rather than a backend playout assertion. Scheduler invariants are verified in Go.

Development note: the host's native file watcher cached transformed files under the hidden worktree, so the final persona pass used a restarted Vite server with file polling. Server restart connection messages are the only browser-console errors from that maintenance interval.

## Reproduction and verification

[Keyboard persona script](persona-keyboard.js) runs with the host `playwright-cli run-code --filename` against an open mock console channel (`?channel=v-80s`). It uses labelled controls, Arrow/Enter menu navigation and Space for switches. The script assigns NEON 84 before its final save.

Final review also replaced smart-filter comma entry with catalogue genre chips and a live result count, exposed source order for movie pools and collections, and removed the inaccurate suggestion that a block personal source is recomputed separately for each viewer. Snapshot boundary tests cover missing, malformed and cold (503) responses, including malformed nested pod data. Per-block padding exposes a filler title picker.

## Guide memory and metadata edits

The unchanged 701-channel fixture uses 12-second and 20-second items. On the
Linux worker, it represented 14,020,000 logical programme rows while storing one
shared 20,000-row table (480,000 bytes). Immutable programme metadata is shared;
time rows are 24 bytes versus 560 bytes for the former full `Programme` value.
Normal IDs are reconstructed per channel, with byte-identical guide output.

Measured incremental peak heap was 42,950,584 bytes (about 41 MiB), below the
512 MiB test bound. Initial warm took 182 ms; the bulk metadata update took
29 ms and retained the existing schedule tables. Metadata-only record saves
also retain them. Availability changes have a separate resolver epoch, including
expiry, so cosmetic edits do not force schedule expansion.

The full Live TV package passed remotely in 57.112 seconds. The console gate
passed 57 tests across Live TV, timeline/layout, scene rendering and all 20 locale
catalogues; typecheck and formatting also passed. All three persona builds used
the real console in mock mode. Preview browsers and the local server were closed.

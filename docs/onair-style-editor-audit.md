# W15 style editor audit

2026-11-05. Console exercised in mock mode at `127.0.0.1:32561/ether/livetv/looks`.
Stable routes and JSON identifiers retain `look`; owner copy uses Styles.
Screenshots and persona records: `thread-storage/thr_cp7svgfc62/v035/gallery-looks-redesign/`.

| Control | Saved state / output path | Preview / disposition |
| --- | --- | --- |
| Describe with model | `/agent/turn`, route `/livetv/look`, `look_draft`, then explicit style Save | Streamed whole draft updates palette, type and moments. Configured-model mock exercised; no external provider called. |
| Describe template | `/onair/template`, deterministic family/mood mapping; no implicit save | Works with disabled agent status. Complete built-in moments; clearly labeled template. Cosy and Retro keywords verified. |
| Revert draft chip | Restores prior whole draft | Live stage changes; joins undo history. |
| Name | `Look.name` | Gallery/drawer title; ident binds preview channel name when previewing a real channel. Both header and manual field labeled. |
| Logo upload / monogram | `Brand.logo`, image asset digest or short monogram | Same owner-logo layer in browser and Go. Accessible upload. |
| Tagline | `Brand.tagline` | Ident/off-air/welcome tag layers bind the brand token, with explicit layer text taking precedence. Channel tagline/accent overrides also preview live. |
| Four role colours | `Brand.palette` | Browser tokens and server tokens; contrast checked live and server rejects text contrast below 4.5:1. |
| Palette preset / shuffle | Palette plus seed | Live output changes. |
| Bundled type pair | `Brand.type` | Bundled browser FontFace and Go font selection. |
| Uploaded type | Font digest in display/body | Authenticated font bytes loaded in browser; Go loads the same font asset. TTF/OTF accepted; unsupported WOFF2 chooser removed. |
| Energy | `Brand.energy` | Shared easing/motion token. |
| Grain | `Brand.textures` | Shared grain token. Unimplemented texture choices removed. |
| Sting | Existing uploaded audio digest | Go encoder uses that audio; stage fetches authenticated bytes through `apiBlob` and plays Audio, without a synthetic substitute. |
| Volume / Sound | Saved volume; preview-only mute | Stage audio follows moment time, seek, loop, pause and half speed; volume applies to sting and authored video. No sting digest means silence. This closes W17's oscillator finding. |
| Corner logo design controls | W13 `BugControls`, saved `Look.bug` | Owner-approved internals retained unchanged, placed in Design. W13 handles Go/browser overlay parity. |
| Seed | `Brand.seed` | Shared deterministic shape/texture token. |
| Moment duration | `Moments[m].duration` | Stage transport and server clip duration. 4/8/12-second buttons. Retro ident changed to 12 s and saved in browser persona. |
| Moment words | `Brand.words` | Offered only when that scene references the corresponding words token. Language selector and unused word inputs removed. |
| Actual layer selection | Selected scene's real IDs, recursively enumerated | Bounded scrolling list; selects properties without changing output. Replaces fabricated universal layer list. |
| Layer text | `Look.layers[id].text` | Same slot resolver in browser/Go; explicit textbox label. `MIDNIGHT MOVIES` and `NEON 84` saved and re-read in browser. |
| Layer fit | `Look.layers[id].fit` | Lowercase shrink/wrap; offered only on text with a width constraint. |
| Layer size / x / y / opacity | Same layer patch fields | Applied by browser and Go. Scene defaults shown until edited. |
| Visibility | `Look.layers[id].hidden` | Persisted patch, rather than local state. Hidden wins over opacity in Go and browser. |
| Keyframes / locks / behaviour / fake timeline | Removed | Old controls had no persisted/rendered behavior. Animation remains authored in family scene documents; arbitrary animation authoring is not claimed. |
| Stage transport | Preview state only | Play/pause, seek, loop, half speed, frame stepping and safe area. Reduced motion starts paused. |
| Moment filmstrip | Preview state only | All supported family moments; changes selected inspector scene. |
| Preview channel / time / viewer | `/onair/bindings` | Actual channels and saved viewers, without demo IDs. Custom time handles an empty input without crashing. |
| Undo / redo / Discard | Whole draft history / saved snapshot | No save until explicit action. Editing is inert while Save is in flight so a late response cannot overwrite a later edit. |
| Save | Existing look POST/PUT | Re-renders in background, render progress retained. New ID transition and repeated layer saves exercised. |
| Duplicate / delete / use on channels | Existing record endpoints / channel saves | Disabled before first save. Channel removals in the assignment picker now remove the matching assignment. |
| Style screens | Optional `Look.screens` | Named style editor owns off-air, can't-play and station ID; shared Save. |
| Owner default | Additive `liveTV.defaultStyleId` | Virtual channels without explicit style inherit it. Empty default preserves existing behavior. Config Save exercised. |
| Channel style / screens | Existing channel style/screen fields | Explicit style beats owner default; channel screen beats style screen; fallback last. Lasting precedence test. |
| Channel bug override | W13 controls | Owner-approved internals retained. |
| Assets search / kind | Library view only | Images/logos, fonts, audio, video clips and collections. Actual collection clips shown individually and open their collection drawer. |
| Asset uploads / font licence | Existing asset endpoints | Immediate library records. Licence required for fonts. |
| Asset usage / removal | Server usage IDs resolved to styles | Style counts shown; used assets cannot be deleted through the UI. Backend remains authoritative. |
| Clip collections | Existing reel endpoints | Live under Assets; refresh when collection drawer closes. No aside footer or main Reels sub-tab. |
| Add from link | W16 `LinkImport` | Placed in Assets; W16 also integrates it in collection clip lists. Queued imports poll; source/status retained. |
| Bumper video / crossfade | W16 moment assetDigest/crossfade | Selected-moment inspector. Ready imported assets included. Authenticated video overlay previews the source and fade. Crossfade disabled without authored video. |
| Break patterns | W16 `BreakPatternEditor`, channel Style Breaks | Kept on `ChannelLook`, the actual persisted model. No unsupported style-level pattern control. |

The editor uses the same family scene documents as the Go renderer, including
the illustrated BLORP!, Nocturne, Meridian, NEON 84 and personal families. The
reference reel and real samples page were opened in the task-owned browser.
The editor refines these scenes through brand tokens, actual layer patches,
words, timing and owner media; it does not expose arbitrary keyframe authoring.

## Persona sessions

| Persona / task | Blockers found | Fixes / final outcome |
| --- | --- | --- |
| Nontechnical owner: cosy late-night movie channel | Describe disabled without model; global Screens ambiguous; edits during a new-record save could be overwritten | Explicit deterministic template path, named style screens, inert editor while saving. Created, saved, edited real ident layer to MIDNIGHT MOVIES, saved and asserted round-trip. Passed. |
| Power user: rebuild NEON 84 and tweak timings | Fabricated layers, decorative keyframes/timeline, fit values did not match renderer, manual field labels missing | Actual scene layers, real property patches, unsupported controls removed, lowercase fit, labels. Created a fresh Retro-family style, renamed it, set ident to 12 s, changed text to NEON 84, saved and asserted round-trip. Passed. |
| Accessibility: keyboard only | Upload controls hidden from keyboard; missing textbox names; animation unsuitable for reduced motion | Focusable visually hidden uploads with focus rings, explicit input labels, stage initially paused under reduced motion. Used only Tab/Enter/type after route setup to describe a calm cinema style, apply its template and save; asserted Play rather than Pause. Passed. |

Additional checks: connected-model mock streamed a complete draft and saved;
channel override and default config save exercised; light/dark, desktop/narrow,
French and all four name options captured. Browser console had no application
errors in the completed personas (Canvas readback performance warnings only).
Dev-server restarts produced expected temporary HMR connection errors outside
those completed sessions.

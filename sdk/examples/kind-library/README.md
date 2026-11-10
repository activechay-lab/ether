# Field Recordings (example)

The content-kind proof example for Ether v0.3.8 (spec §13): a
three-level "field recordings" kind (collection → album → track,
tracks playable) with fixture content. It exists so the kind
infrastructure is exercised end to end by tests; it is **not** a
shipped feature and is not in the signed catalogue.

It exercises: a hierarchical declaration with an indexed field
(`genre`), pull sync with `/changes`, live search
(`browse.liveSearch`), the container pattern (tracks extracted from
one zip per album), a `{pageNumber}` URL template, a title matcher,
one setup step (source address + API key), presentations for
MediaBrowser (`music`) and Plex (`show`), and a tiny OPDS-ish
frontend adapter.

`kind_library_test.go` (in `sdk/examples`) packs it, checks it, runs
it hosted, syncs it into the plugin-item store, opens track 2's
bytes through the container pattern, and pins a manual match.
Assertions that need slice D (installing into a full test Ether,
browsing through a client surface, removal purging the library) live
in `TestKindLibraryEndToEndSliceD`, skipped until D lands.

See [docs/plugin-kinds.md](../../../docs/plugin-kinds.md).

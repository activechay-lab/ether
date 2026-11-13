# Console visual review fixtures

Run `pnpm dev:mock` in web and add `?review` to the initial URL. This opts into
illustrative watchlists, per-friend sync outcomes, a poster folder listing,
unavailable plugin item, Plex step-aside status/explanation, and log SSE snapshots.
These fixtures never run in production mode and do not contact a server.

Set `sessionStorage["ether.review.state"]` to `done`, `running`, `empty`, `failed`,
or `pin` to choose a watchlist scenario. Reload with `?review` still present; console
navigation may remove the query. Running sync jobs remain running until the scenario
changes. This is a visual fixture, not a persistence or backend conformance test.

The mock agent recognizes “Create a family movie channel” and presents a draft and
approval. Approval records the choice without creating a channel.

The v0.3.10 image index lives in coordinator thread storage:
`thr_daerp5pbp7/shots-0310/INDEX.md`.

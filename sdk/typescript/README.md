# `@ether/plugin-sdk` (TypeScript / JavaScript)

Authoring kit for Ether plugins targeting Ether Plugin Protocol v1 (EPP/1).

The same `plugin({ manifest, routes })` definition runs:
- **Hosted** inside Ether's embedded QuickJS + wazero sandbox (`ether.runtime.kind: "hosted"`), bundled into a single artifact with `ether plugin pack`.
- **As a sidecar** on Node or Bun (`ether.runtime.kind: "remote"`), either serving HTTP directly (`serve(p, { mode: "http" })`) or dialing outbound over the `ether-tunnel-v1` WebSocket tunnel (`serve(p, { mode: "tunnel" })`).

Hosted plugins and Studio previews have no direct host filesystem, environment,
process or network access. QuickJS `std` and `os` modules are unavailable.
Use the permission-checked `ether` host APIs for services such as HTTP and
folders. Remote sidecars run under their own operating-system permissions and
are not covered by the hosted sandbox.

## Usage

```ts
import { plugin, serve } from "@ether/plugin-sdk";

const hello = plugin({
  manifest: {
    id: "org.ether.examples.hello",
    name: "Hello plugin",
    version: "0.1.0",
    description: "Minimal greeting plugin for Ether.",
    types: ["movie"],
    idPrefixes: ["tt"],
    catalogs: [{ type: "movie", id: "hello", name: "Hello movies" }],
    resources: ["catalog", "meta"],
    ether: {
      protocol: 1,
      kind: "extension",
      capabilities: ["list_source", "ui"],
      summary: "Minimal greeting plugin for Ether.",
      runtime: { kind: "hosted", entry: "plugin.ts" },
    },
  },
  routes: {
    catalog({ type, id }) {
      return {
        metas: [
          {
            id: "tt0133093",
            type,
            name: `Hello from ${id}`,
            releaseInfo: "1999",
          },
        ],
      };
    },
    meta({ type, id }) {
      return {
        meta: {
          id,
          type,
          name: `Title ${id}`,
          releaseInfo: "1999",
          description: "Sample metadata entry.",
        },
      };
    },
  },
});

export const handle = hello.handle;
export const rank_key = hello.rank_key;
export const cond_match = hello.cond_match;
export default hello;

// In a separate Node/Bun entry point: await serve(hello);
```

## Capabilities and routes

- `catalog` (`GET /catalog/{type}/{id}[/{extra}].json`)
- `meta` (`GET /meta/{type}/{id}.json`)
- `stream` (`GET /stream/{type}/{id}[/{extra}].json`) and `openStream` (`POST /ether/stream/open`)
- `subtitles` (`GET /subtitles/{type}/{id}[/{extra}].json`, requires `X-Ether-Pick` header)
- `markers` (`GET /ether/markers/{type}/{id}.json`)
- `channels` (`GET /ether/channels.json`) and `guide` (`GET /ether/guide/{channelId}.json`)
- `notify` (`POST /ether/notify`)
- `event` (`POST /ether/event`)
- `view` (`GET /ether/view/{page}`) and `action` (`POST /ether/action/{action}`)
- `tool` (`POST /ether/tool/{name}`) and `job` (`POST /ether/job/{name}`)
- `file` (`GET /ether/file/{path...}`)
- `rankKey` and `condMatch` hot-path exports (`rank_key`, `cond_match`)

Tunnel mode currently makes one connection. Use a process supervisor to
restart the sidecar after a disconnection.

## Sidecars

A sidecar is a remote plugin whose program runs on a machine you choose and
dials out to Ether over the `ether-tunnel-v1` WebSocket, so it needs no inbound
port. Pair once and the SDK saves the credential:

```sh
ETHER_URL=https://ether.example.com \
ETHER_PLUGIN_STATE_DIR=/data \
ETHER_PAIRING_CODE=PAIR-1234 \
node server.js
```

Create the code in Ether under Plugins › your plugin › Connect a sidecar; it is
valid for 5 minutes and single use. `serve(plugin, { mode: "tunnel" })` resolves
the credential in this order: an explicit credential, then the saved file, then
the pairing code (exchanged once and saved). With none it throws
`CredentialError` with one clear line.

- `ETHER_URL` — the server's `http(s)` base; the pair and `ws(s)` tunnel URLs
  are derived from it.
- `ETHER_PLUGIN_CREDENTIAL` — an explicit credential; always wins.
- `ETHER_PLUGIN_STATE_DIR` — where the credential is saved, else `/data` when it
  exists and is writable (a Docker volume), else `~/.config/ether-plugins/<id>/`.
- `ETHER_PAIRING_CODE` — a one-time code.

The credential is saved at `<state dir>/credential` (mode `0600`, directory
`0700`). If the tunnel rejects a saved credential, the file is deleted and
`serve` throws `CredentialError` instead of reconnecting forever.

A Dockerfile that keeps the credential across restarts:

```dockerfile
FROM node:22-slim
WORKDIR /app
COPY . .
VOLUME /data
ENV ETHER_PLUGIN_STATE_DIR=/data
CMD ["node", "server.js"]
```

Declare how the console runs it with an `ether.sidecar` manifest block:

```json
"ether": {
  "runtime": { "kind": "remote" },
  "sidecar": {
    "docker": "ghcr.io/you/my-sidecar:1.0.0",
    "docs": "https://example.com/my-sidecar"
  }
}
```

`docker` is an image reference; `download` (an https URL) with `command` (a
single line) is the alternative; `docs` adds a setup link.

Searchable settings: a `select` field with `search: true` shows an explicit search
control in the installed plugin's Settings drawer. Ether forwards `q` and
`locale` to `GET /ether/options/{key}` in `request.query` (array values when hosted). Return an array of
`{value, label}` choices. The chosen string is stored verbatim; a JSON string can
carry coordinates and a display name together. Search never saves settings.
Background requests receive the server process locale (`LC_ALL`, `LC_MESSAGES`,
then `LANG`) in the `X-Ether-Locale` header when the caller has not supplied one.

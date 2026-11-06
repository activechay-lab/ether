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

Searchable settings: a `select` field with `search: true` shows an explicit search
control in the installed plugin's Settings drawer. Ether forwards `q` and
`locale` to `GET /ether/options/{key}` in `request.query` (array values when hosted). Return an array of
`{value, label}` choices. The chosen string is stored verbatim; a JSON string can
carry coordinates and a display name together. Search never saves settings.
Background requests receive the server process locale (`LC_ALL`, `LC_MESSAGES`,
then `LANG`) in the `X-Ether-Locale` header when the caller has not supplied one.

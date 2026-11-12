# `etherplugin` (Go SDK)

Go authoring package (`github.com/braedonsaunders/ether/sdk/go`) for Ether Plugin Protocol v1 (EPP/1).

## Execution modes

1. **Native HTTP or WebSocket tunnel sidecar (`ether.runtime.kind: "remote"`)**:
   Compile a standard Go binary and call `p.Serve(ctx, etherplugin.ServeOptions{})` (or mount `p` as an `http.Handler`). Host calls go over HTTP to `/ether/plugin-host/*` authenticated with `X-Ether-Plugin-Credential`.
2. **Hosted WebAssembly guest**: not yet supported. Go 1.27.3 `GOOS=wasip1 GOARCH=wasm` emits `wasi_snapshot_preview1` imports (including `proc_exit`, `clock_time_get`, and `fd_write`), which the runtime forbids. TinyGo is not installed here, so a compatible TinyGo build has not been verified. Use a sidecar until a guest is built and tested against Ether's raw WASM ABI.

## Usage

```go
package main

import (
	"context"
	"os/signal"
	"syscall"

	etherplugin "github.com/braedonsaunders/ether/sdk/go"
)

func main() {
	p := etherplugin.New(etherplugin.Definition{
		Manifest: etherplugin.Manifest{
			ID:      "org.ether.examples.hello",
			Name:    "Hello plugin",
			Version: "0.1.0",
			Ether: &etherplugin.EtherManifestBlock{
				Protocol:     1,
				Kind:         etherplugin.KindExtension,
				Capabilities: []etherplugin.Capability{etherplugin.CapListSource, etherplugin.CapUI},
				Summary:      "Minimal greeting plugin for Ether.",
				Runtime:      etherplugin.RuntimeDecl{Kind: "remote"},
			},
		},
		Routes: etherplugin.Routes{
			Catalog: func(ctx *etherplugin.Context, req etherplugin.CatalogRequest) (etherplugin.CatalogResponse, error) {
				return etherplugin.CatalogResponse{
					Metas: []etherplugin.CatalogItem{{
						ID:          "tt0133093",
						Type:        req.Type,
						Name:        "Hello from " + req.ID,
						ReleaseInfo: "1999",
					}},
				}, nil
			},
		},
	})

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	_ = p.Serve(ctx, etherplugin.ServeOptions{})
}
```

## WS8b interoperability test

Once `internal/plugins/remote` is present, run
`go test -tags ws8b ./sdk/examples/hello/go -run TestWS8bSDKInterop`.
It starts Ether's tunnel server and exercises the Go, Python, and TypeScript
hello sidecars. It requires Python 3 and a Node version with TypeScript support
and WebSocket. The Go SDK reconnects automatically after a dropped tunnel.

## Tunnel recovery

`Start(ctx, ServeOptions{Mode: "tunnel", ...})` returns after the initial
credential authentication and hello/welcome handshake; initial failures are
returned to the caller. After that, dropped connections retry serially with
jittered exponential backoff (50–100 ms initially, capped at 5–10 seconds).
Every attempt re-authenticates and repeats the handshake, with a 10-second
connection/handshake timeout. A connection stable for a minute resets backoff.

In-flight calls fail on disconnect and are not replayed. Ether runtimes resolve
the current connection for subsequent calls. `handle.Done` stays open during
recovery; cancelling `ctx` or calling `handle.Close()` stops active I/O and any
retry, closes the socket, and closes `Done`. `Close` is safe to call repeatedly.
Request cancellation stops that handler without closing the shared tunnel.

## Sidecars

A sidecar is a remote plugin whose program runs on a machine you choose and
dials out to Ether over the `ether-tunnel-v1` WebSocket. It needs no inbound
port. Use one when the program must reach a local network, a USB device, or an
installed tool that Ether's sandbox cannot.

Set `ETHER_URL` and pair once:

```sh
ETHER_URL=https://ether.example.com \
ETHER_PLUGIN_STATE_DIR=/data \
ETHER_PAIRING_CODE=PAIR-1234 \
./my-sidecar
```

The SDK exchanges the code once, saves the credential, and derives the tunnel
URL from `ETHER_URL`. Create the code in Ether under Plugins › your plugin ›
Connect a sidecar; it is valid for 5 minutes and single use. On later restarts
the saved credential is reused, so no new code is needed.

- `ETHER_URL` — the server's `http(s)` base. The pair URL and the `ws(s)` tunnel
  URL are derived from it.
- `ETHER_PLUGIN_CREDENTIAL` — an explicit credential; always wins.
- `ETHER_PLUGIN_STATE_DIR` — where the credential is saved, else `/data` when it
  exists and is writable (a Docker volume), else the OS user config directory
  `…/ether-plugins/<plugin id>/`.
- `ETHER_PAIRING_CODE` — a one-time code.

The credential is saved at `<state dir>/credential` (mode `0600`, directory
`0700`) as JSON with the Ether URL it belongs to. If the tunnel rejects a saved
credential, the file is deleted and startup fails with one line instead of
reconnecting forever.

A Dockerfile that keeps the credential across restarts:

```dockerfile
FROM golang:1.27 AS build
WORKDIR /src
COPY . .
RUN go build -o /out/sidecar ./cmd/sidecar

FROM gcr.io/distroless/static
COPY --from=build /out/sidecar /sidecar
VOLUME /data
ENV ETHER_PLUGIN_STATE_DIR=/data
ENTRYPOINT ["/sidecar"]
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
single line) is the alternative; `docs` adds a setup link. With a Docker image
the console shows a ready `docker run` command, mounting `-v ether-<id>:/data`
so the saved credential survives a restart.

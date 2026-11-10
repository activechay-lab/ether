# Contributing to Ether

Thank you for contributing to Ether.

## Cloning the repository

Use a blobless partial clone for a fast checkout without downloading historical assets:

```sh
git clone --filter=blob:none https://github.com/braedonsaunders/ether.git
cd ether
```

Git fetches commit and tree metadata up front and downloads file blobs on demand, keeping history and commit SHAs intact while avoiding historical bundle downloads.

## Building from source

### Full server with the `/ether/` web console

Building a complete `ether` binary with the embedded React owner console requires:

- **Go** (version in [`go.mod`](go.mod))
- **Node.js 22+** and **pnpm 10** (`corepack enable && corepack prepare pnpm@10 --activate`)
- **FFmpeg / FFprobe** on `PATH` or next to the binary for transcoding and media probing (`make ffmpeg` stages the pinned portable build into `dist/ffmpeg/`)

Build the web console and binaries together:

```sh
make build
```

Or run the steps directly:

```sh
scripts/build-web.sh
CGO_ENABLED=0 GOEXPERIMENT=simd go build -trimpath -o dist/ether ./cmd/ether
```

`scripts/build-web.sh` installs locked dependencies in `web/` and outputs the production bundle to `internal/admin/dist/`, which `internal/admin` embeds via `//go:embed all:dist`. Generated files under `internal/admin/dist/` are gitignored (only `.gitkeep` is tracked), so `git status` stays clean after building.

### Go-only builds and tests (no Node required)

You do **not** need Node or `pnpm` to work on Go packages, protocol frontends, streaming, or tests:

```sh
CGO_ENABLED=0 go build ./...
CGO_ENABLED=0 go vet ./...
go test ./internal/...
```

When compiled without running `make web` / `scripts/build-web.sh`, the server, all media protocols, and the `/ether/api/` JSON endpoints run normally, and opening `/ether/` in a browser serves a short placeholder page showing how to build the console.

### Web console development

```sh
cd web
pnpm install --frozen-lockfile
pnpm dev            # Vite dev server
pnpm typecheck      # tsc -b
pnpm test           # Vitest unit tests
pnpm format:check   # Prettier check
```

If you add or change user-facing strings in `web/src/i18n/en.ts`, check translation coverage with `pnpm i18n todo`.

### Docker builds

The multi-stage [`Dockerfile`](Dockerfile) compiles the web console automatically inside a `node:22-alpine` stage before building the Go server. Stage the pinned Linux FFmpeg binaries first, then build:

```sh
scripts/fetch-ffmpeg.sh linux-$(go env GOARCH)
docker build -t ether:dev .
```

## Checks before opening a pull request

- **Do not commit generated UI bundles.** Leave `internal/admin/dist/` untouched (`internal/admin/dist/.gitkeep` only). CI builds `web/` once and verifies Go embeds the resulting bundle.
- **Formatting and linters:** run `make lint` (`gofmt`, `go mod tidy -diff`, `go vet`, `golangci-lint` on new changes, and `shellcheck` on shell scripts).
- **Targeted tests:** run `go test` on the packages you changed (and `pnpm typecheck && pnpm test` in `web/` for console changes).
- **Legal boundaries:** Ether ships no media content, no providers, no indexers, and no third-party web client code. Read [`LEGAL.md`](LEGAL.md) before submitting protocol or integration changes.

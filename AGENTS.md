# AGENTS.md

This file provides guidance to the AI agent when working with code in this repository.

## Project shape

Two Go entry points share `internal/` packages:

- `main.go` — Wails v2 desktop app. Embeds `web/dist` via `//go:embed all:web/dist`, starts MITM proxy on `:9528` and AI Gateway on `:9090`. Uses `~/.lingma-tap/` for CA, SQLite, and `app.log`.
- `cmd/server/main.go` — headless gateway server (Docker). Uses `$DATA_DIR` (fallback `./data`); reads auth from `$DATA_DIR/auth/`.

Frontend lives under `web/` (React 19 + Vite 6 + Tailwind v4). Wails-generated bindings under `web/wailsjs/` and Vite output `web/dist/` are gitignored — never hand-edit them.

## Build, run, test

- Dev: `wails dev` (Wails dev URL is hardcoded to `http://localhost:5173`; `vite.config.ts` sets `strictPort: true`, so kill anything else on 5173 first).
- Desktop build: `wails build` (macOS) or `wails build -platform windows/amd64`.
- Server build: `go build -o server ./cmd/server` (or `docker-compose up --build`).
- Tests: `go test ./...`. There are no frontend tests configured.
- Frontend-only build: `cd web && npm run build` (runs `tsc -b && vite build`). `tsconfig.json` enables `strict`, `noUnusedLocals`, `noUnusedParameters`, `noUncheckedSideEffectImports` — unused imports/locals fail the build.

Before `wails build` on a fresh checkout, `web/dist/` must exist or the `//go:embed` will fail; `wails build` handles this, but plain `go build ./...` will not.

## Conventions

- Commits: Conventional Commits (`feat:`, `fix:`, `chore:`, `docs:`, `refactor:`, `test:`). Mixed English/Chinese subjects are accepted.
- Go: standard `gofmt`. Tests live next to code as `*_test.go`. Integration tests under `internal/bridge/*_test.go`.
- No linter is configured for either side. Do not introduce one without asking.
- SQLite migrations are SQL files in `internal/storage/migrations/` (`NNNNNN_name.up.sql` / `.down.sql`), applied via `golang-migrate`. Add new migrations as the next `NNNNNN`; never edit applied ones.

## Runtime quirks

- `GATEWAY_DEBUG=1` enables verbose gateway/SSE logging. Useful when debugging bridge behavior.
- The Anthropic/OpenAI bridge initializes only when credentials are present; if absent, `bridgeHandlerField` stays nil and bridge endpoints stay disabled — handle the nil path, don't assume it exists.
- The MITM proxy requires CA trust on the host. CA path is exposed through `App.GetCACertPath()`.
- Auth is file-based (`~/.lingma-tap/auth/` for desktop, `$DATA_DIR/auth/` for server) or uploaded via `POST /api/auth/upload`. Treat these files as secrets — never commit, never log their contents.

## Other

- An `AGENTS.local.md` at the repo root is picked up with higher priority than this file and is gitignored — use it for personal workflow notes.
- Subdirectory `AGENTS.md` files (e.g. under `web/` or `internal/bridge/`) are loaded automatically when working in those trees if more focused guidance is ever needed.

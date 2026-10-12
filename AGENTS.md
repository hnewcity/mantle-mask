# Repository Guidelines

## Project Structure & Module Organization

Mantle Mask combines a Go OpenAI-compatible Bedrock Mantle gateway with a React/TypeScript console.

- `cmd/mantle-mask/`: executable entry point.
- `internal/`: HTTP routes (`server`), upstream routing and streaming (`gateway`), configuration and credentials (`config`), and usage storage (`usage`).
- `web/src/`: pages, shared components, and utilities; `web/public/` contains static assets. Vite builds `web/dist/`, which `web/embed.go` embeds into the binary.
- Go tests sit beside source files; frontend unit tests live in `web/src/`, and browser/SDK tests in `web/e2e/`.
- `scripts/` contains development and test helpers; `docs/` covers architecture, research, and verification.

## Build, Test, and Development Commands

Run from the repository root in Linux/WSL2. Race tests require GCC and libc development dependencies.

| Command | Purpose |
| --- | --- |
| `bash scripts/dev.sh` | Start Go on port 8080 and Vite with hot reload on 5173. |
| `make build` | Install frontend dependencies, build assets, and produce `bin/mantle-mask`. |
| `./bin/mantle-mask` | Run the compiled service and embedded console. |
| `make test` | Run Go race tests, `go vet`, Vitest, and TypeScript checks. |
| `npm --prefix web run test:e2e` | Run Playwright against isolated gateway and mock upstream fixtures. |

Before E2E tests, run `make build`, then install Chromium with `cd web && npx playwright install --with-deps chromium`.

## Coding Style & Naming Conventions

Format Go with `gofmt` (tabs).

Use strict TypeScript, two-space indentation, double quotes, and omit statement-ending semicolons. Use PascalCase for page files and React components; keep shared UI filenames lowercase. Prefer `@/` imports within `web/src/`. Reuse existing UI components and Chinese console copy conventions. No ESLint or Prettier configuration is present.

## Testing Guidelines

Use Go's `testing` package with `Test...` functions in `*_test.go`, Vitest in `*.test.ts`, and Playwright in `*.spec.ts`. Use temporary directories and mock upstreams. Cover changed authentication, routing, persistence, and SSE behavior; no numeric coverage threshold is configured. Run applicable checks before submitting.

## Commit & Pull Request Guidelines

Use concise imperative subjects, such as `Fix response affinity routing`. Keep commits focused. PRs should explain behavior changes, link relevant issues, list validation commands and results, and include screenshots for console changes.

## Security & Configuration

Keep credentials, `.env`, and runtime `data/` out of commits. Native execution requires exported environment variables; it does not load `.env`. Keep real AWS smoke tests opt-in because inference incurs usage.

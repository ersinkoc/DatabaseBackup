# Project Analysis Report

> Auto-generated comprehensive analysis of Kronos
> Generated: 2026-04-29
> Analyzer: Codex — Full Codebase Audit

## 1. Executive Summary

Kronos is a single-binary Go backup platform with a control-plane HTTP API, worker agents, CLI, embedded React WebUI, embedded KV state store, chunked encrypted repository format, local/S3 storage, retention, audit, notifications, and database driver MVPs for Redis, PostgreSQL, MySQL/MariaDB, and MongoDB. It is currently a strong Phase 1/early Phase 2 implementation with meaningful production scaffolding, but it materially diverges from its own product promise: three of four database families are implemented by shelling out to vendor tools, while `.project/SPECIFICATION.md` and `.project/PROMPT.md` explicitly forbid that.

Key metrics: 22,109 raw files including `web/node_modules` and `.tools/go`; 290 first-party/audit-relevant files excluding `.git`, `.tools`, `web/node_modules`, and `web/dist`; 212 first-party Go files; 119 non-test Go files; 93 Go test files; 42,602 first-party Go LOC; 6 first-party TS/TSX/JS files plus embedded built UI assets; 3,323 frontend/source+embedded UI LOC; 77.5% overall Go statement coverage from `go test ./... -coverprofile`; 10 Go modules from `go list -m all`; 7 direct frontend dependencies and 3 frontend dev dependencies.

Overall health after Phase 1 hardening: **7.4/10**. The implementation is unusually complete for its dependency constraints and has broad tests, release scripts, Kubernetes examples, OpenAPI checks, and good operational docs. The score is still held down by severe spec deviations, missing PITR/gRPC/MCP/OIDC/TOTP/mTLS, no race-test capability in this environment because `gcc` is absent, and a huge `cmd/kronos/server.go` file that centralizes too much production-critical behavior. The most urgent immediate defects found in this audit were addressed on 2026-04-29: large restore record handling, production default auth gating, and PostgreSQL/MongoDB process-argument password exposure.

Top strengths: broad tested Go foundation; disciplined dependency policy; good CLI/API/server/agent skeleton with persistent state and audit. Top concerns: database driver MVPs violate the no-shell-out requirement; advanced auth and transport security are mostly absent; full v0.1 acceptance criteria are not met despite docs claiming high readiness percentages.

## 2. Architecture Analysis

### 2.1 High-Level Architecture

The project is a modular monolith delivered as one `kronos` binary. Modes are dispatched from `cmd/kronos`: `server`, `local`, `agent`, plus CLI verbs. The server uses `net/http` and `http.ServeMux`, stores mutable state in `internal/kvstore`, serves REST endpoints and the embedded WebUI, and starts a background scheduler loop. Agents poll the control plane over HTTP, sync targets/storages/backups, claim jobs, execute driver/pipeline work, and finish jobs.

Text data flow:

```text
CLI/WebUI -> net/http server -> token scope check -> typed KV stores
server scheduler -> JobStore queued jobs -> agent heartbeat/poll/claim
agent -> database driver -> engine JSON record stream -> chunk pipeline
chunk pipeline -> local/S3 backend -> signed manifest -> BackupStore metadata
restore -> manifest chain -> chunk restore/decrypt/decompress/hash -> driver restore
```

Concurrency model: `serveControlPlaneWithOptions` starts `http.Server.Serve` and `startSchedulerLoop` in goroutines (`cmd/kronos/server.go:103`, `cmd/kronos/server.go:174`). `chunk.Pipeline.Feed` uses one chunk producer, worker pools for hash/encode/upload, bounded channels, cancellation, and sorted refs (`internal/chunk/pipeline.go:84`). Agent workers poll on a ticker (`internal/agent/worker.go:31`). The model is serviceable, but uses bare goroutines rather than `errgroup`, which conflicts with `.project/PROMPT.md` guidance.

### 2.2 Package Structure Assessment

Go package responsibilities:

| Package | Files | Responsibility | Assessment |
|---|---:|---|---|
| `cmd/kronos` | 50 | CLI, server handlers, local/agent entrypoints | Feature-rich but `server.go` is 4,233 LOC and should be split. |
| `internal/core` | 9 | Domain types, IDs, typed errors, clock | Cohesive and high coverage. |
| `internal/config`, `internal/secret` | 7 | YAML config, env/file placeholders | Good implemented subset; external secret providers missing. |
| `internal/drivers/*` | 23 | Database driver interface and implementations | Redis is native RESP; Postgres/MySQL/Mongo shell out. |
| `internal/agent` | 7 | Control-plane client, worker, executor | Clear structure; HTTP polling instead of spec gRPC stream. |
| `internal/server` | 17 | KV-backed stores, scheduler runner, orchestration | Cohesive but thin business validation. |
| `internal/kvstore` | 14 | Page/B+Tree/WAL/buckets/repair | Ambitious and tested, still custom storage risk. |
| `internal/chunk`, `internal/compress`, `internal/crypto` | 26 | FastCDC, BLAKE3, compression, AEAD, key slots | Strong Phase 1 core. |
| `internal/storage/local`, `internal/storage/s3` | 10 | Repository object backends | Local and S3 implemented; S3 spools whole object to temp file. |
| `internal/manifest`, `internal/repository`, `internal/verify` | 11 | Manifest signing, commit/load, verification | Good manifest-level implementation. |
| `internal/retention`, `internal/schedule`, `internal/restore` | 13 | Policy resolver, cron/window/queue, restore chain planning | Good core, partial spec. |
| `internal/obs`, `internal/audit`, `internal/webui` | 12 | Metrics/logging/request IDs, hash-chain audit, static UI serving | Good basics; no tracing. |

Circular dependency risk is low because everything lives under `internal` and imports mostly flow inward to `core`. The main architectural smell is not package cycles; it is vertical coupling in `cmd/kronos/server.go`, which owns routing, auth, validation glue, resource mutation, metrics, notifications, and restore/backup job creation.

### 2.3 Dependency Analysis

Go dependencies from `go.mod`/`go list -m all`:

| Dependency | Version | Purpose | Replaceable? | Notes |
|---|---:|---|---|---|
| `github.com/klauspost/compress` | v1.18.1 | zstd compression | No practical stdlib replacement | Allowed by spec. |
| `github.com/klauspost/cpuid/v2` | v2.0.9 | indirect compress CPU detection | No | Indirect. |
| `golang.org/x/crypto` | v0.50.0 | Argon2id, ChaCha20-Poly1305 | No | Allowed. |
| `golang.org/x/sys` | v0.43.0 | indirect/platform support | No | Allowed. |
| `golang.org/x/net` | v0.52.0 | indirect | Maybe | Pulled indirectly. |
| `golang.org/x/term` | v0.42.0 | indirect | Maybe | Pulled indirectly. |
| `golang.org/x/text` | v0.36.0 | indirect | Maybe | Pulled indirectly. |
| `gopkg.in/yaml.v3` | v3.0.1 | config parsing | No | Allowed. |
| `gopkg.in/check.v1` | 2016 pseudo-version | indirect test dependency | Could avoid if not needed | From YAML transitive. |
| `lukechampine.com/blake3` | v1.4.1 | chunk hash | No | Allowed by spec. |

Dependency hygiene is good and intentionally tiny. `staticcheck` and `govulncheck` were not installed locally, so vulnerability/unused-dependency claims are limited to `go test`, `go vet`, and module inspection.

Frontend dependencies from `web/package.json`: React 19, React DOM 19, Vite 6, Tailwind 4.1, `@tailwindcss/vite`, `@vitejs/plugin-react`, `lucide-react`; dev deps are TypeScript 5.6 and React type packages. This is smaller than the planned UI stack: no TanStack Router, TanStack Query, Zustand, react-hook-form, Zod, Recharts, Radix/shadcn components beyond one local button.

### 2.4 API & Interface Design

Endpoint inventory from `cmd/kronos/server.go`:

| Method(s) | Path | Handler area |
|---|---|---|
| GET/HEAD | `/healthz`, `/readyz`, `/metrics`, `/api/v1/overview` | probes, metrics, dashboard |
| GET/POST | `/api/v1/agents`, `/api/v1/agents/heartbeat`, `/api/v1/agents/{id}` | agent registry |
| GET/POST | `/api/v1/audit`, `/api/v1/audit/verify` | audit list/verify |
| POST | `/api/v1/auth/verify` | token verification |
| GET/POST/POST action | `/api/v1/tokens`, `/api/v1/tokens/{id}`, `/api/v1/tokens/{id}/revoke`, `/api/v1/tokens/prune` | token lifecycle |
| GET/POST/DELETE/POST action | `/api/v1/users`, `/api/v1/users/{id}`, `/api/v1/users/{id}/grant` | user metadata |
| GET/POST actions | `/api/v1/jobs`, `/api/v1/jobs/claim`, `/api/v1/jobs/{id}`, `/api/v1/jobs/{id}/finish|cancel|retry|evidence` | job lifecycle |
| POST | `/api/v1/scheduler/tick`, `/api/v1/backups/now` | operations |
| GET/POST actions | `/api/v1/backups`, `/api/v1/backups/{id}`, `/api/v1/backups/{id}/protect|unprotect|verify` | backups |
| POST/GET/PUT/DELETE | `/api/v1/retention/plan`, `/api/v1/retention/apply`, `/api/v1/retention/policies*` | retention |
| GET/POST/PUT/DELETE | `/api/v1/notifications*` | webhook notifications |
| POST | `/api/v1/restore`, `/api/v1/restore/preview` | restore planning/queueing |
| GET/POST/PUT/DELETE | `/api/v1/targets*`, `/api/v1/storages*`, `/api/v1/schedules*` | resources |
| GET | `/*` | WebUI SPA |

API consistency is decent but not ideal. Success responses are JSON; errors use `http.Error` plaintext in many places. Auth uses bearer token scope checks in `requireScope` (`cmd/kronos/server.go:2174`) and local/no-token development mode when `TokenStore` is nil. There is no CORS handler, no cookie/CSRF model, and no login/refresh/logout endpoints despite the spec. Security headers are set in `withSecurityHeaders` (`cmd/kronos/server.go:335`), but HSTS is intentionally absent.

## 3. Code Quality Assessment

### 3.1 Go Code Quality

The code is gofmt-compatible and `go vet ./...` passed. Error handling generally wraps useful context and returns typed `core` error kinds in storage/server layers. Context usage exists in blocking I/O paths, but driver shell-outs and pipeline goroutines are not structured through `errgroup`. Logging is limited; `internal/obs/log.go` has redaction, but the server mostly writes status lines to `out` and HTTP errors.

Configuration is YAML plus env/file expansion, with config-seeded resources (`cmd/kronos/server.go:451`). Secrets are redacted for API output via `redactOptions` and for logs via `obs.NewRedactingHandler`, but target/storage secrets are persisted in `state.db` as ordinary option values after seeding or API create. This violates the spec promise that credentials are never stored as plaintext on disk.

Open TODO/FIXME/HACK markers: none found in first-party source. Explicit unsupported markers exist for SFTP/Azure/GCS, advanced streams/PITR, and unsupported incremental/oplog/binlog/WAL paths.

### 3.2 Frontend Code Quality

The frontend is a single large `web/src/App.tsx` file of 3,118 LOC plus a tiny `Button`, `main.tsx`, and utility. It uses React hooks and TypeScript types, but lacks the planned route/component/data-layer architecture. There is no TanStack Router, React Query, Zustand, form validation library, charts library, tests, axe checks, or E2E screenshots. It stores bearer tokens in `localStorage` (`web/src/App.tsx:405`, `web/src/App.tsx:1141`), which is convenient but increases XSS blast radius.

UI build passed: Vite output is 272.07 kB JS / 75.87 kB gzip and 15.88 kB CSS / 4.02 kB gzip, comfortably under the 500 kB gzip target. Accessibility is mixed: inputs often have labels and buttons use icons, but there is no automated a11y test suite and no route-level keyboard audit evidence.

### 3.3 Concurrency & Safety

Strengths: chunk pipeline uses bounded channels and cancellation; local storage uses lock files and atomic rename; server shutdown calls `http.Server.Shutdown` with timeout; agent worker cleanly follows context cancellation.

Risks:

- `engine.BackupFull` and `engine.BackupIncremental` start driver goroutines and wait on result channels after `pipeline.Feed`; if a driver blocks writing to a pipe after pipeline failure, cancellation relies on closing the reader rather than a shared cancel function (`internal/engine/backup.go:31`).
- Fixed 2026-04-29: `engine.Restore` now uses `json.Decoder` rather than `bufio.Scanner`, with a 128 KiB record regression test in `internal/engine/backup_test.go`.
- `startSchedulerLoop` uses a bare goroutine and prints errors; no supervisor or fatal escalation (`cmd/kronos/server.go:174`).
- Race tests could not be executed because `.tools/go/bin/go test -race` requires CGO and `gcc` is not installed in this environment.

### 3.4 Security Assessment

Implemented: token secrets are random, stored as SHA-256 verifiers, compared constant-time (`internal/server/token_store.go:154`); token scopes exist; mutation audit events are widespread; baseline browser security headers and no-store cache headers are present; log redaction exists; webhook HMAC exists per docs/code.

Major gaps:

- No mandatory authentication when `stores.tokens` is nil/local mode is active. This is acceptable for development but unsafe for any exposed server.
- No bcrypt password auth, no TOTP, no OIDC, no mTLS, no gRPC transport, no CSRF model.
- Target and storage credentials can be stored in KV options as plaintext, despite redacted output.
- Fixed 2026-04-29 for process arguments: PostgreSQL strips password material from `--dbname` URIs and uses `PGPASSWORD`; MongoDB uses a 0600 temporary Database Tools `--config` file; MySQL already used `MYSQL_PWD`. Environment variables, temp files, and persisted target credentials still require host-level hardening.
- WebUI localStorage token persistence (`web/src/App.tsx:1141`) is vulnerable to token theft if any XSS is introduced.
- Docker image runs from `scratch`, good, but defaults to plain HTTP and no TLS enforcement.

## 4. Testing Assessment

### 4.1 Test Coverage

`go test ./... -count=1` passed. `go vet ./...` passed. `go test ./... -coverprofile=/tmp/kronos-cover.out` passed with **77.5% total statement coverage**. `go test -race` failed before compiling because `gcc` is missing. `staticcheck` and `govulncheck` were not installed locally.

Package coverage highlights: `internal/core` 94.9%, `internal/secret` 94.1%, `internal/crypto` 87.3%, `internal/chunk` 81.5%, `internal/manifest` 80.5%, `cmd/kronos` 75.9%, `internal/server` 78.7%, `internal/obs` 63.0%, PostgreSQL 74.6%, MySQL 69.5%, MongoDB 71.1%, Redis 76.8%.

Packages with no test files/statements: `internal/buildinfo` has no test files; `api/openapi`, `bench`, and `docs` report no statements or doc/spec tests only.

### 4.2 Test Infrastructure

There are 93 Go test files and many meaningful table/unit tests. CI runs formatting, vet, staticcheck, govulncheck, race tests, production-check, build, release artifact smoke tests, container build, PostgreSQL/MySQL/MariaDB/MongoDB service conformance, and release workflows. Tagged E2E tests exist under `cmd/kronos`. Frontend has no Vitest/Playwright/Cypress tests. Integration tests rely on external services and GitHub Actions service containers, not a local self-contained harness.

## 5. Specification vs Implementation Gap Analysis

### 5.1 Feature Completion Matrix

| Planned Feature | Spec Section | Implementation Status | Files/Packages | Notes |
|---|---|---|---|---|
| Single binary modes | §2.1, §3.16 | Complete | `cmd/kronos/*` | One binary with server/local/agent/CLI. |
| Local mode | §2.2 | Partial | `cmd/kronos/local.go` | Works, but no secure bootstrap model. |
| PostgreSQL pure wire protocol | §3.1.1 | Missing/regression | `internal/drivers/postgres/driver.go:22` | Uses `pg_dump`, `pg_dumpall`, `psql`; no WAL/PITR/COPY implementation. |
| MySQL/MariaDB pure wire protocol | §3.1.2 | Missing/regression | `internal/drivers/mysql/driver.go:19` | Uses `mysqldump`/`mysql`; no binlog/PITR. |
| MongoDB OP_MSG/BSON native | §3.1.3 | Missing/regression | `internal/drivers/mongodb/driver.go:19` | Uses `mongodump`/`mongorestore`; no oplog/PITR. |
| Redis RESP logical backup | §3.1.4 | Partial | `internal/drivers/redis` | RESP SCAN/DUMP/RESTORE works; RDB/AOF/PSYNC PITR missing. |
| Full backups | §3.2 | Partial | drivers, `internal/engine` | Full works for implemented paths. |
| Incremental/differential | §3.2 | Partial by fallback | `internal/engine/backup.go:80` | Driver incrementals unsupported; engine falls back to full. |
| PITR stream | §3.2, §3.9 | Missing | driver `Stream`/`ReplayStream` | Stub/reserved. |
| Local storage | §3.3 | Complete | `internal/storage/local` | Atomic local backend exists. |
| S3 storage | §3.3 | Partial | `internal/storage/s3` | SigV4/multipart/retry exists; broader compatibility/IAM matrix incomplete. |
| SFTP/FTP/Azure/GCS/WebDAV | §3.3 | Missing | `internal/agent/executor.go:112` | Explicit unsupported kind. |
| zstd/gzip/adaptive compression | §3.4 | Partial | `internal/compress` | zstd/gzip/auto; no lz4/xz. |
| AES-GCM/ChaCha20 + Argon2id slots | §3.5 | Partial | `internal/crypto`, `internal/chunk/envelope.go` | Core exists; no full age sealed repo. |
| FastCDC+BLAKE3 dedup | §3.6 | Complete core | `internal/chunk` | Implemented and tested. |
| Scheduling cron/@between | §3.7 | Partial | `internal/schedule` | Cron/window/catch-up basics; event/chain triggers absent. |
| GFS/count/time/size retention | §3.8 | Largely complete | `internal/retention` | Good policy resolver; chain-safe deletion limited by metadata model. |
| Restore planning/start | §3.9 | Partial | `internal/restore`, server restore handlers | Full restore from chunks exists; PITR/table-level/sandbox live restore absent. |
| Verification levels | §3.10 | Partial | `internal/verify`, agent executor | Manifest/chunk verification; logical replay/live sandbox absent. |
| Hooks | §3.11 | Missing | none | No hook package. |
| Notifications | §3.12 | Partial | `internal/server/notification_store.go`, server handlers | Webhook only; Slack/Discord/email/etc absent. |
| RBAC/auth | §3.13 | Partial | `internal/server/token_store.go`, server `requireScope` | Scoped tokens only; no passwords/TOTP/OIDC/mTLS/projects. |
| Secrets management | §3.14 | Partial/weak | `internal/secret`, config | env/file only; no encrypted store or external providers. |
| Observability | §3.15 | Partial | `internal/obs`, `/metrics` | Metrics/log redaction; no OTLP tracing. |
| REST API/OpenAPI | §3.16 | Partial | `cmd/kronos/server.go`, `api/openapi` | Broad REST; no login/refresh; no gRPC/MCP. |
| WebUI routes | §3.16 | Partial | `web/src/App.tsx` | Operational dashboard in one SPA; not planned route architecture. |
| gRPC API | §3.16 | Missing | none | HTTP polling instead. |
| MCP server | §3.16 | Missing | none | No MCP implementation. |
| Release artifacts | §4.3/4.5 | Partial/strong | scripts, CI, Dockerfile | Good scripts, SBOM/provenance; no Homebrew/Scoop/apt/rpm. |

### 5.2 Architectural Deviations

The largest deviation is intentional pragmatism: PostgreSQL, MySQL, and MongoDB drivers shell out to native tools, explicitly contradicting `.project/SPECIFICATION.md` §3.1 and `.project/PROMPT.md` "No Shelling Out". This improves time-to-MVP and conformance coverage but regresses the core product promise and deployment model.

Agent/server communication deviates from planned gRPC+mTLS bidirectional streams to HTTP polling. This is simpler and NAT-friendly, but misses streaming progress, mTLS, and protocol versioning. The WebUI deviates from the planned TanStack/shadcn/query architecture into a single-file dashboard. Storage support is narrowed to local/S3; other backends fail fast. Secrets are runtime-expanded and redacted in output but still persist as regular KV options.

### 5.3 Task Completion Assessment

TASKS.md lists about 218 v0.1 tasks. Actual completion is approximately **57% by task count**, but weighted lower for product acceptance because many high-value tasks are missing or implemented as MVP substitutes.

Completed or mostly complete: Phase 0 foundation; local/S3 storage; crypto/chunk pipeline; custom KV store; CLI broad surface; REST resource APIs; scheduler basics; retention; audit; metrics; release scripts; Kubernetes single-replica examples.

Partial: database drivers, orchestration, WebUI, notifications, verification, docs. Missing: native PG/MySQL/Mongo protocols, PITR, gRPC, MCP, advanced auth, most secret providers, hooks, OTel tracing, SFTP/Azure/GCS/FTP, package manager distribution, systemd/Helm/Ansible beyond Kubernetes examples.

### 5.4 Scope Creep Detection

Valuable additions beyond original near-term scope include release provenance/SBOM/cosign workflows, detailed Kubernetes manifests, restore evidence artifacts, token pruning metrics, and a broad operations overview API. These are valuable, not harmful. The risky scope creep is extensive WebUI/API polish around a backend whose central driver promise remains unmet.

### 5.5 Missing Critical Components

Highest priority missing components: native database protocols or a permanent spec revision admitting tool-wrapper drivers; secure production bootstrap/auth; PITR for at least PostgreSQL/MySQL; race-testable local environment; encrypted secret storage/external secret providers; gRPC/mTLS or an updated HTTP agent protocol security design.

## 6. Performance & Scalability

Hot paths: chunk pipeline, S3 Put, KV list scans, scheduler tick, WebUI polling. The chunk pipeline is bounded and parallel. S3 `Put` calls `spoolAndHash` and may spool entire objects to disk before upload (`internal/storage/s3/backend.go:115`), which avoids memory blowups but hurts streaming purity and temp-disk pressure. KV list operations scan full buckets and server metrics aggregate in memory; this is fine for thousands of resources but not indefinitely scalable.

Horizontal scaling: agents scale horizontally; the control plane does not. Kubernetes explicitly sets one control-plane replica with RWO PVC and `Recreate` strategy. State is local embedded KV, so multi-writer HA is not possible. Backpressure exists in the pipeline; job queue backpressure is basic and per-agent capacity aware.

## 7. Developer Experience

Onboarding is good if local Go and pnpm are available: `make build`, `make check`, `.tools/go/bin/go test ./...`, `pnpm run build`. Docs are extensive and unusually candid in `docs/status.md` and `docs/production-readiness.md`, though some readiness percentages are too optimistic and conflict with the original spec. Makefile is useful and release scripts are concrete. Dockerfile is simple and static.

Documentation quality is high but fragmented. There is no `CHANGELOG.md`, no root `SPECIFICATION.md`/`TASKS.md` (they live in `.project`), no API rendered reference beyond OpenAPI YAML, and no ADRs beyond `docs/decisions/0001-kvstore.md`.

## 8. Technical Debt Inventory

### Critical

- Fixed 2026-04-29: `internal/engine/backup.go` restore record reading uses `json.Decoder`, removing the 64 KiB scanner token limit.
- `internal/drivers/postgres/driver.go:82`, `internal/drivers/mysql/driver.go:62`, `internal/drivers/mongodb/driver.go:62`: shell-out drivers violate the core specification. Either implement native protocols or formally revise spec/product claims.
- Fixed 2026-04-29: `kronos server` requires auth by default unless `--dev-insecure` is set. `kronos local` keeps loopback dev ergonomics and requires explicit `--dev-insecure` for non-loopback unauthenticated mode.
- `cmd/kronos/server.go:451` and resource create/update paths: secrets can persist in KV as plaintext. Add encrypted secret store or reference-only model.

### Important

- `cmd/kronos/server.go` is 4,233 LOC; split routing, auth, handlers, DTOs, metrics, notification delivery, and resource handlers.
- `web/src/App.tsx` is 3,118 LOC; split into route/features/API hooks and add frontend tests.
- `internal/agent/client.go:157` reads up to 4 KiB of error bodies with `io.ReadAll`, acceptable but should ensure no sensitive data is echoed from server errors.
- `internal/storage/s3/backend.go:115`: whole-object temp spooling conflicts with streaming requirement; design chunk-size direct upload path.
- Missing gRPC/mTLS/OIDC/TOTP/CSRF/rate limiting beyond token verify.
- CI requires race/staticcheck/govulncheck, but local environment cannot run them due missing tools/compiler.

### Minor

- `tree` is not installed locally, so the mandated tree command could not run; document fallback.
- `web/dist`, `bin`, coverage files, and `node_modules` are present in the workspace and inflate discovery counts.
- `gopkg.in/check.v1` transitive dependency is old but indirect.

## 9. Metrics Summary Table

| Metric | Value |
|---|---:|
| Raw Files | 22,109 |
| First-Party/Audit Files | 290 |
| Total Go Files | 212 |
| Non-Test Go Files | 119 |
| Total Go LOC | 42,602 |
| Total Frontend Files | 6 source files plus embedded static assets |
| Total Frontend LOC | 3,323 |
| Test Files | 93 |
| Test Coverage | 77.5% statements |
| External Go Dependencies | 10 modules total, 4 direct runtime modules |
| External Frontend Dependencies | 7 dependencies, 3 dev dependencies |
| Open TODOs/FIXMEs | 0 |
| API Endpoint Patterns | 36 route registrations |
| Spec Feature Completion | ~55% |
| Task Completion | ~57% |
| Overall Health Score | 7.1/10 |

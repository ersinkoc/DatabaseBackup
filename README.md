# Kronos

**Time devours. Kronos preserves.**

Kronos is a zero-dependency Go binary for scheduled, encrypted, verified
database backups. The product brief and build plan live in `.project/`:

- [Specification](.project/SPECIFICATION.md)
- [Implementation](.project/IMPLEMENTATION.md)
- [Tasks](.project/TASKS.md)
- [Branding](.project/BRANDING.md)
- [Architecture](ARCHITECTURE.md)
- [Quick start](docs/quickstart.md)
- [CLI reference](docs/cli.md)
- [Operations runbook](docs/operations.md)
- [Deployment topologies](docs/deployment-topologies.md)
- [Restore drills](docs/restore-drills.md)
- [Release verification](docs/release-verification.md)
- [Cloud secret integration](docs/cloud-secrets.md)
- [Project status](docs/status.md)
- [Production readiness](docs/production-readiness.md)
- [Kubernetes example](deploy/kubernetes/README.md)

This repository currently has the Phase 0 foundation in place and active Phase
1/early Phase 2 implementation work:

- storage backends: local filesystem and S3-compatible object storage; SFTP,
  Azure Blob, and Google Cloud Storage are domain-level roadmap kinds, not
  executable backends in this build
- crypto/chunk core: FastCDC, BLAKE3 chunk IDs, compression, encryption
  envelopes, dedup index, backup/restore pipeline
- repository metadata: signed manifests, manifest commit/load helpers, manifest
  and chunk-level verification
- embedded state: pure-Go page store, B+Tree, rollback WAL, buckets, repair
- audit: kvstore-backed hash-chained append log with verification and server
  mutation recording
- retention: count, time, size, and GFS policy resolver with JSON plan output
- scheduling: 5-field/6-field cron parser, `@between` windows with stable
  jitter, catch-up policies, per-target queueing, and persisted server-side
  scheduler ticks/background loop
- driver scaffold: generic driver interfaces plus executable Redis/Valkey
  SCAN/DUMP/RESTORE support with ACL snapshot/restore records and JSON command
  stream replay. PostgreSQL has a logical `pg_dump`/`psql` MVP, and
  MySQL/MariaDB has a `mysqldump`/`mysql` logical MVP with real-service MySQL
  8.4, MariaDB 11.4, and bidirectional MySQL/MariaDB restore rehearsal
  coverage in CI, plus 10,000-row MySQL and MariaDB restore drills. MongoDB
  remains a roadmap driver in this build.
- CLI: dispatcher, version, database repair,
  backup now/list/inspect/protect/unprotect/verification,
  target/storage add/list/inspect/update/remove, schedule add/list/inspect/pause/resume/remove,
  target test, storage test/du, scheduler tick, overview, jobs list/inspect/cancel/retry, audit
  list/tail/search/verify, token create/list/inspect/verify/revoke/prune,
  notification add/list/inspect/update/remove, retention
  plan/apply/policy add/list/inspect/update/remove, key slot/escrow/rotation helpers,
  restore preview/start,
  user add/list/inspect/remove/grant,
  health/readiness, metrics, overview, and repository garbage collection, plus bash/zsh/fish completion
  generation
- backup listing filters by target, storage, type, protection state, and
  timestamp windows such as `--since 7d`
- job listing filters by status, operation, target, storage, agent, and queued
  time windows
- audit listing, tailing, and searching filters by actor, action, resource, and
  occurred-at windows
- server: config-loading HTTP skeleton with `/healthz`, `/readyz`, `/metrics`,
  probe-friendly `HEAD` handling, and graceful shutdown
- local mode: starts an embedded control-plane server with local state and can
  run an embedded worker with `--work`
- WebUI: embedded React/Tailwind operations dashboard served by the control
  plane, with a Vite build pipeline and deployment-safe cache headers
- agent/server: heartbeat endpoint, list/inspect APIs, in-memory agent
  registry, and heartbeat-only or worker-mode agent process
- agent worker: control-plane HTTP client with resource sync, heartbeat, job
  claim, finish reporting, full/incremental backup execution, and restore
  execution
- server state: kvstore-backed job persistence, restart recovery for active
  jobs, config-seeded resources, `/api/v1/jobs`, claim, finish, and cancel
  endpoints
- orchestration: scheduler due jobs persist as queued jobs with
  queued/running/terminal lifecycle transitions and backup metadata capture on
  successful finish
- REST API: target, storage, schedule CRUD endpoints and manual
  `POST /api/v1/backups/now`, operations overview at `/api/v1/overview`,
  with a checked OpenAPI spec in `api/openapi/`
- backups API: list/inspect/protect/unprotect plus server-side retention
  policy CRUD and plan/apply with dry-run support
- restore API: restore preview plans that validate backup parent chains and
  enqueue restore jobs with dry-run and replace-existing restore options
- notification API: webhook rule CRUD for terminal job events with optional HMAC
  payload signatures and bounded delivery retries
- token API: scoped API token create/list/verify/revoke/prune with hashed verifier
  storage and copy-once bearer secret output
- user API: local user metadata create/list/get/delete plus role grants
- HTTP hardening: request IDs, no-store control-plane responses, and baseline
  browser security headers for API/WebUI responses

## Build

```bash
make build
./bin/kronos version
```

`make build` stamps version metadata from Git when available. Override
`VERSION`, `COMMIT`, or `BUILD_DATE` for reproducible release builds.
`make release` writes a platform-named binary and matching `.sha256` checksum
under `bin/`; `make release-all` writes linux/darwin amd64/arm64 artifacts by
default.

Build and embed the WebUI before producing a UI-enabled binary:

```bash
make ui
make build
```

Container builds use the same stamped metadata:

```bash
docker build \
  --build-arg VERSION="$(git describe --tags --always --dirty 2>/dev/null || echo dev)" \
  --build-arg COMMIT="$(git rev-parse --short HEAD 2>/dev/null || echo unknown)" \
  --build-arg BUILD_DATE="$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
  -t kronos:local .
```

## Check

```bash
make check
```

In this workspace, a local Go toolchain is available at `.tools/go/bin/go`, so
the equivalent direct check is:

```bash
.tools/go/bin/go test ./...
```

Inspect expanded configuration safely with redacted secret-like fields:

```bash
./bin/kronos --output pretty config inspect --config kronos.yaml
```

Structured command output supports `--output json`, `--output pretty`,
`--output yaml`, and `--output table`; `--output` can be placed before the
command or alongside command flags.
CLI color is enabled only for terminals. Use `--no-color` or `NO_COLOR=1` for
plain output in logs and scripts.

## API Tokens

Create a scoped token and use it from the CLI. Local/no-token development mode
still works without an `Authorization` header; when a bearer token is provided,
the server enforces exact scopes plus `resource:*`, `admin:*`, or `*`.

```bash
./bin/kronos user add --id admin --email admin@example.com --display-name Admin --role admin
./bin/kronos token create --user admin --name ci --scope backup:read,backup:write
export KRONOS_TOKEN=<copy-once-secret>
./bin/kronos token verify
./bin/kronos --server http://127.0.0.1:8500 --token "$KRONOS_TOKEN" backup list
```

After rotating credentials, preview and prune inactive token metadata:

```bash
./bin/kronos token prune --dry-run
./bin/kronos token prune
```

Common scope families are `backup`, `target`, `storage`, `schedule`, `job`,
`retention`, `restore`, `audit`, `token`, `user`, `agent`, and `metrics`, each
using `:read` or `:write` where applicable. `overview` uses `metrics:read`.
Requested token scopes are capped by the token user's role.

Agents can use the same bearer secret through `KRONOS_TOKEN` or
`kronos agent --token <secret>` and advertise concurrency with
`--capacity <n>`. Worker agents need `agent:write`, `job:write`,
`target:read`, `storage:read`, and `backup:read` for heartbeat, resource sync,
claim, and finish calls.

Run `kronos agent --work` to claim and execute jobs. Worker mode syncs targets,
storages, and backups from the control plane and needs a manifest signing key
plus a chunk encryption key:

```bash
./bin/kronos keygen --key-id prod-2026
export KRONOS_MANIFEST_PRIVATE_KEY=<ed25519-private-key-hex>
export KRONOS_CHUNK_KEY=<32-byte-hex-key>
./bin/kronos agent --work --server http://127.0.0.1:8500 --token "$KRONOS_TOKEN" --key-id prod-2026
```

Config secret placeholders support environment variables and files, including
structured JSON/YAML selectors such as `${file:secrets.yaml#database.password}`.

## Verify A Backup Manifest

Manifest existence/signature check:

```bash
./bin/kronos backup verify \
  --manifest manifest.json \
  --public-key <ed25519-public-key-hex> \
  --storage-local /path/to/repo
```

Chunk integrity check, including decrypt/decompress/hash verification:

```bash
./bin/kronos backup verify \
  --manifest-key manifests/2026/04/23/backup-1.manifest \
  --level chunk \
  --chunk-key <32-byte-decryption-key-hex> \
  --public-key <ed25519-public-key-hex> \
  --storage-local /path/to/repo
```

## Repository GC

Dry-run unreferenced chunk collection:

```bash
./bin/kronos gc \
  --storage-local /path/to/repo \
  --public-key <ed25519-public-key-hex> \
  --dry-run
```

## Retention Planning

```bash
./bin/kronos retention plan --input retention-plan.json
```

The input JSON contains `policy`, `backups`, and an optional `now` timestamp.
The output marks each backup as kept or droppable with rule reasons.

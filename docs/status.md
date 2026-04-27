# Project Status

Last reviewed from the repository state on April 27, 2026.

Kronos is currently a working Phase 1 / early Phase 2 backup platform rather
than a bare scaffold. The core control plane, CLI, state store, scheduler,
agent worker, backup pipeline, restore planning, retention engine, audit log,
OpenAPI contract, operations documentation, and observability surface are in
place and covered by tests.

## Current Shape

```mermaid
flowchart LR
    CLI[kronos CLI]
    WebUI[Embedded WebUI]
    Server[Control plane]
    Agent[Worker agent]
    KV[(Embedded state DB)]
    Repo[(Backup repository)]
    Redis[(Redis target)]
    Postgres[(PostgreSQL target)]

    CLI --> Server
    WebUI --> Server
    Server --> KV
    Server --> Agent
    Agent --> Redis
    Agent --> Postgres
    Agent --> Repo
```

## Completed Foundations

- Single Go binary with server, local, agent, and administrative CLI modes.
- Embedded kvstore with WAL, B+Tree buckets, repair coverage, and persisted
  control-plane stores.
- Local and S3-compatible storage backends.
- Chunking, deduplication, compression, encryption envelopes, signed manifests,
  and manifest/chunk verification.
- Redis backup and restore driver coverage, including ACL and command-stream
  replay paths.
- PostgreSQL logical driver MVP using `pg_dump` plain SQL output for full
  backups and `psql` for restores.
- Persistent scheduler and queued/running/terminal job lifecycle.
- Agent worker resource sync, heartbeat, job claim, backup execution, restore
  execution, and finish reporting.
- REST API for resources, jobs, backups, retention, restore, audit, users, and
  tokens, with checked OpenAPI coverage.
- Token-based authorization with scoped bearer tokens, role-capped token
  creation, inactive token pruning, request IDs, and audit recording for
  mutations.
- Baseline HTTP security headers for API and embedded WebUI responses.
- Webhook notification rules for terminal job events, API management, optional
  HMAC payload signatures, bounded retries, and delivery metadata in the audit
  chain.
- Dashboard-oriented operations overview API with inventory counts, active job
  state, backup totals, readiness state, attention counters, and latest activity
  slices.
- Readiness, health, Prometheus metrics with `GET`/`HEAD` probe support,
  operations docs, CLI docs, quickstart, architecture docs, deployment topology
  guidance, restore drill guidance, multi-platform release artifacts, checksums,
  provenance metadata, SBOM metadata, release artifact smoke checks, container
  builds, GitHub release publishing, Kubernetes deployment examples, and cloud
  secret integration guidance.

## Recent Progress

```mermaid
flowchart TB
    Metrics[Observability hardening]
    Ready[Readiness endpoint and CLI]
    Docs[Status and operations documentation]

    Metrics --> BuildInfo[kronos_build_info]
    Metrics --> Uptime[process start and uptime metrics]
    Metrics --> Inventory[inventory distribution metrics]
    Metrics --> Jobs[job operation and agent load metrics]
    Metrics --> BackupFreshness[latest backup freshness metrics]
    Metrics --> Tokens[token revoked/expired metrics]
    Metrics --> Alerts[Prometheus alert examples]

    Ready --> ReadyEndpoint[/readyz]
    Ready --> Stores[persistent store checks]
    Ready --> ReadyCLI[kronos ready]
    Ready --> Completion[completion coverage]

    Docs --> OpenAPI[OpenAPI descriptions]
    Docs --> Ops[operations runbook]
    Docs --> Status[project status snapshot]
```

## Verification State

The repository currently passes the full Go test suite:

```bash
.tools/go/bin/go test ./...
```

The production-readiness gate is also available without relying on `make`:

```bash
GO=.tools/go/bin/go ./scripts/production-check.sh
```

This gate checks Go formatting, runs `go vet`, runs the full test suite, builds
the binary, validates shell scripts, validates generated bash completion
syntax, and verifies that `kronos version` can execute. The docs test also
checks local Markdown links, and the OpenAPI package has a checked spec test.
The current explicit `not implemented` markers are
intentional fail-fast boundaries for roadmap database drivers and storage
backends; unsupported capabilities are surfaced early instead of falling
through to ambiguous runtime behavior.

Tagged E2E coverage is available for the implemented worker/control-plane/Redis
backup and restore path, retention apply metadata pruning, and lost-agent
recovery during job claim. The same tagged suite also covers server restart
recovery for active jobs reopened from persisted state, plus PostgreSQL
worker/control-plane/local-storage backup and restore smoke coverage through
fake `pg_dump` and `psql` tools:

```bash
.tools/go/bin/go test -tags=e2e ./cmd/kronos
```

## Known Gaps

See [Production Readiness](production-readiness.md) for the current release
gate, readiness estimate, and next engineering slices.

Kronos is usable for its implemented Redis/local/S3-oriented paths, and now has
a PostgreSQL logical driver MVP. It is not yet a broad multi-database
production suite. The largest remaining areas are:

- PostgreSQL real-service conformance in CI. Current PostgreSQL driver coverage
  includes command-runner unit coverage over `pg_dump` and `psql`, tagged
  worker pipeline smoke E2E coverage with fake client tools, and an opt-in
  real-service conformance test gated by `KRONOS_POSTGRES_TEST_DSN`.
- Additional database drivers such as MySQL and MongoDB. Current executable
  driver coverage is Redis/Valkey plus the PostgreSQL logical MVP.
- Additional storage backends such as SFTP, Azure Blob, and Google Cloud
  Storage. Current executable backends are local filesystem and S3-compatible
  object storage.
- Deeper WebUI interaction against real API data beyond the current embedded
  dashboard shell.
- Richer notification channels and hook execution surfaces from the product
  plan.
- Broader production hardening around auth integrations and multi-instance
  operational patterns.

## Next Best Work

1. Wire PostgreSQL real-service conformance into CI with a service container
   and verify restore data on every protected branch run.
2. Wire the WebUI to live API endpoints for dashboard state, jobs, backups, and
   agents.
3. Add additional notification channels and hook execution surfaces.
4. Sign release provenance and SBOM metadata with keyless CI identity.
5. Add cloud-specific deployment manifests for common managed Kubernetes
   environments.

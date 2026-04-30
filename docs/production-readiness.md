# Production Readiness

Last reviewed from the repository state on April 30, 2026.

Kronos is close to production-ready for the implemented Redis or Valkey backup
path with local or S3-compatible storage. A PostgreSQL logical backup/restore
MVP is now present through `pg_dump` and `psql`, with worker/control-plane/local
repository smoke E2E coverage and real-service PostgreSQL conformance running
in CI against PostgreSQL 15, 16, and 17. That conformance now covers
extension-backed data, large objects, restore guardrails, rollback behavior for
failed restores, a 2,500-row indexed JSONB restore rehearsal, and optional
PostgreSQL global role metadata capture through `include_globals=true` without
leaking role password material, plus a focused `postgres_globals` restore drill
for role metadata. CI also runs a PostgreSQL 15-to-17 restore rehearsal and a
PostgreSQL 16-to-17 restore rehearsal, plus a dedicated PostgreSQL 17 full
global restore rehearsal that replays the actual `pg_dumpall --globals-only`
stream plus database stream into a separate target.
CI also runs a PostgreSQL 17 operator-scale restore drill with 10,000 indexed
JSONB rows across separate source and target services. It still lacks native
WAL/PITR support before it should be treated as a fully production-grade
PostgreSQL path. MongoDB now has a `mongodump`/`mongorestore` archive MVP with
deterministic unit coverage, explicit replace-existing restore guardrails,
failed restore temp-config cleanup coverage, authenticated real-service MongoDB
7.0/8.0 conformance, and an authenticated MongoDB 7.0 10,000-document restore
drill plus a MongoDB 7.0 replica-set/oplog recovery drill in CI. It still
depends on MongoDB Database Tools archive/oplog semantics rather than native
continuous oplog streaming. The full product vision across SFTP, Azure Blob,
Google Cloud Storage, deeper WebUI workflows beyond the authenticated live
overview/jobs/backups/inventory dashboard plus
target/storage/schedule/retention/job/backup detail, schedule pause/resume, job
cancel/retry, target/storage/schedule/retention create/update editing, guarded
target/storage deletion, manual backup drill queueing, backup metadata
verification, byte-level backup verification queueing, verification result
display, backup verification history, restore preview plus guarded dry-run/live
restore queueing, restore job history with restore outcome summaries and
hash-addressed evidence artifacts, and backup protection actions, and
multi-instance control-plane operation is still roadmap work. The Kubernetes
manifests encode the supported single-replica boundary with a Recreate rollout,
one PVC-backed state store, and a control-plane disruption budget.
MySQL/MariaDB
now has a `mysqldump`/`mysql` logical MVP with deterministic unit coverage and
real-service MySQL 8.4 plus MariaDB 11.4 conformance for backup/restore of
indexed JSON data. CI also runs bidirectional MySQL 8.4 and MariaDB 11.4
cross-engine restore rehearsals with larger indexed JSON datasets, and a
10,000-row operator-scale restore drill for both MySQL and MariaDB. Larger
field-scale MySQL/MariaDB drills remain before that path should be treated as
fully production-grade.

## Readiness Estimate

| Scope | Estimate | Notes |
| --- | ---: | --- |
| Implemented Redis/local/S3 path | 95% | Core pipeline, TLS/mTLS-capable agent/control-plane transport, lost-agent recovery, server restart recovery, restore planning, retention, notifications, schedule hooks, audit, metrics, release scripts, single-replica Kubernetes examples with EKS/GKE/AKS overlays, runbooks, a reusable production gate, and tagged worker/control-plane/Redis backup, restore, hook, retention apply, and recovery E2E tests are in place. |
| Broad multi-database product vision | 96% | Redis is executable, PostgreSQL now has a plain SQL logical driver MVP, optional global role metadata capture, full global restore coverage, worker pipeline smoke E2E coverage, CI conformance coverage across PostgreSQL 15, 16, and 17, a PostgreSQL 15-to-17 restore rehearsal, and a 10,000-row PostgreSQL restore drill. MySQL/MariaDB now has a `mysqldump`/`mysql` logical MVP with unit coverage, real-service MySQL 8.4 and MariaDB 11.4 conformance, bidirectional MySQL/MariaDB restore rehearsal coverage, and 10,000-row MySQL/MariaDB restore drills. MongoDB now has a `mongodump`/`mongorestore` archive MVP with unit coverage, authenticated real-service MongoDB 7.0/8.0 conformance, failed restore cleanup coverage, an authenticated MongoDB 7.0 10,000-document restore drill, and a MongoDB 7.0 replica-set/oplog recovery drill, while storage backends, WebUI workflows, native MongoDB oplog streaming, and multi-instance deployment patterns remain roadmap work. |
| Current repository release hygiene | 99% | Tests, vet, format checks, OpenAPI checks, release artifacts, provenance, SBOM metadata, SBOM module verification, GitHub build/SBOM attestations, keyless cosign signatures and verification, signed-tag preflight/verification scripts, consumer release verification docs, CI govulncheck, release-gated SBOM vulnerability verification, release artifact smoke checks, missing/corrupted chunk verification failure drills, notification fan-out and hook execution tests, PostgreSQL full global restore, PostgreSQL operator-scale restore, MySQL, MariaDB, bidirectional MySQL/MariaDB restore rehearsal conformance, 10,000-row MySQL/MariaDB restore drills, authenticated MongoDB 7.0/8.0 service conformance, authenticated MongoDB 7.0 operator-scale restore drill, MongoDB 7.0 replica-set/oplog recovery drill, the production check script, tagged backup/restore/hook/retention/recovery E2E coverage, and Node 24-native GitHub Actions are present. The `golang.org/x/crypto` advisories are fixed. |

## Current Release Gate

Use this command before a release candidate:

```bash
GO=.tools/go/bin/go ./scripts/production-check.sh
```

The gate checks formatting, runs `go vet`, runs the full Go test suite, builds
the binary, validates shell scripts, validates bash completion syntax, and
executes `kronos version`.

Race testing requires CGO and a working C compiler such as `gcc` or `clang`.
If the compiler is absent, `go test -race ./...` cannot start; run the normal
test gate as a fallback, but do not treat that as equivalent to a clean race
run.

## Production-Ready Strengths

- Core streaming backup pipeline with chunking, compression, encryption
  envelopes, signed manifests, restore planning, and verification.
- Redis/Valkey driver coverage with backup and restore paths.
- PostgreSQL logical driver MVP using `pg_dump` for full backups and `psql` for
  restores, with password material stripped from process-visible `--dbname`
  arguments and passed through `PGPASSWORD`, deterministic command-runner unit tests, tagged worker
  pipeline smoke E2E coverage, CI real-service conformance coverage across
  PostgreSQL 15, 16, and 17,
  extension-backed data, large object checks, indexed JSONB bulk restore
  checks, optional
  `pg_dumpall --globals-only --no-role-passwords` role metadata capture with a
  real-service conformance assertion, focused `postgres_globals` restore checks
  for role metadata, a PostgreSQL 17 full global restore rehearsal that replays
  actual globals plus database streams into a separate target, `replace_existing`
  enforcement for non-dry-run restores, single-transaction `psql` execution,
  rollback verification for failed restores, PostgreSQL 15-to-17 and 16-to-17
  restore rehearsals, and a PostgreSQL 17 operator-scale restore drill that
  verifies 10,000 indexed JSONB rows across separate source and target services.
- MySQL/MariaDB logical driver MVP using `mysqldump` for full backups and
  `mysql` for restores, with password material passed through `MYSQL_PWD`
  instead of command arguments and unit coverage for backup, restore,
  replace-existing guardrails, dry-run behavior, and unsupported incremental
  paths. CI now runs real-service MySQL 8.4 and MariaDB 11.4 conformance that
  creates a source database, backs it up with `mysqldump` or `mariadb-dump`,
  restores into a separate database, and verifies indexed JSON row counts and
  checksums. Separate MySQL 8.4 to MariaDB 11.4 and MariaDB 11.4 to MySQL 8.4
  restore rehearsals verify larger 1,500-row indexed JSON datasets across
  engines. Dedicated MySQL 8.4 and MariaDB 11.4 operator-scale restore drills
  verify 10,000 indexed JSON rows across separate source and target services.
- MongoDB logical driver MVP using `mongodump --archive` for database-scoped
  full backups and `mongorestore --archive --drop` for restores, with unit
  coverage for target tests, archive records, namespace remapping,
  replace-existing guardrails, dry-run behavior, failed restore error
  propagation and temp-config cleanup, process-list password exposure avoidance
  through 0600 temporary Database Tools `--config` files, and an opt-in
  full replica-set `mongodump --oplog` / `mongorestore --oplogReplay` command
  path. CI now runs
  authenticated MongoDB 7.0/8.0 real-service conformance with archive
  backup/restore into a remapped database, indexed document count/checksum
  verification, and an authenticated MongoDB 7.0 10,000-document restore drill
  across separate source and target services, plus a MongoDB 7.0 replica-set
  oplog archive/replay drill against disposable source and target replica sets.
- Local and S3-compatible storage backends.
- Persistent control plane state, scheduler state, jobs, backups, retention,
  notifications, users, tokens, and audit log.
- Configured notification rules can fan out one event to multiple webhook
  channels, and scheduled backup jobs can carry pre-backup and failure hooks to
  the claiming agent for shell or webhook execution.
- Restore evidence artifacts are hash-addressed and stored independently from
  job records, so `/api/v1/jobs/{id}/evidence` remains available after job
  metadata pruning.
- First-admin bootstrap for empty user/token stores, optional
  `server.auth.bootstrap_token` protection for the one-time bootstrap endpoint,
  scoped bearer tokens, role-capped token creation, current-role enforcement on
  every bearer-token request, token lifecycle operations, request IDs, security
  headers, and mutation audit events.
- Local password authentication and TOTP are not part of the implemented auth
  surface. User records reject `totp_enforced=true` until a password/TOTP login
  flow exists.
- Optional state DB encryption for sensitive target/storage option values via
  `server.master_passphrase`.
- Direct control-plane TLS with optional client-certificate verification through
  `server.tls.cert`, `server.tls.key`, and `server.tls.client_ca`; agents can
  provide `KRONOS_TLS_CA`, `KRONOS_TLS_CERT`, and `KRONOS_TLS_KEY` for private
  CA trust and mTLS enrollment.
- Agent-side resolution for full-value target/storage secret placeholders,
  CLI `*-ref` helper flags for managed resource credentials, and API
  validation that rejects malformed target/storage placeholder syntax.
- Consistent JSON error envelopes for REST failures, including machine-readable
  status code, human-readable message, and request ID correlation.
- Store-level resource validation for ID shape, target driver enums, storage
  kind/URI scheme compatibility, schedule backup types, cron/window
  expressions, retention rule kinds, and scalar target/storage option schemas.
- Configurable control-plane request body cap plus read-header, read, write,
  and idle HTTP server timeouts.
- Health, readiness, metrics, OpenAPI, operations docs, deployment topology
  docs, single-replica Kubernetes deployment examples, restore drill docs,
  release verification docs, release scripts, provenance metadata, SBOM
  metadata, GitHub build/SBOM attestations, keyless cosign release signatures
  and verification.
- CI runs formatting, vet, staticcheck, govulncheck, race tests, PostgreSQL
  15/16/17 service conformance, PostgreSQL 15-to-17 and 16-to-17 restore rehearsals,
  PostgreSQL 17 full global restore rehearsal, PostgreSQL 17 operator-scale
  restore drill, MySQL 8.4 and MariaDB 11.4 service conformance,
  bidirectional MySQL/MariaDB restore rehearsals, 10,000-row MySQL/MariaDB
  restore drills, authenticated MongoDB 7.0/8.0 service conformance,
  authenticated MongoDB 7.0 10,000-document restore drill, release artifact
  verification, SBOM module verification with required `govulncheck`, container builds, completion
  syntax checks, and the
  production-readiness gate. Release
  artifacts are also smoke-tested by
  executing the host binary and validating generated shell completion.
- Tagged E2E coverage exercises a control-plane HTTP server, worker agent,
  local repository storage, and Redis-compatible RESP target together for
  backup and restore. It also covers scheduled pre-backup shell/webhook hook
  execution, failure hook execution with `KRONOS_JOB_ERROR`, retention apply
  over committed backup metadata, including dry-run behavior, deletion,
  protected backup retention, retained backup-chain protection, and mutation
  audit recording. Lost-agent recovery is covered through heartbeat, claim,
  failed running job, target unblock, and recovery audit behavior. Server
  restart recovery is covered through persisted running/finalizing jobs,
  boot-time recovery, HTTP job inspection, and post-shutdown state verification.
  PostgreSQL worker smoke coverage exercises backup and restore through fake
  `pg_dump`/`psql` tools, the control plane, local storage, manifests, and the
  restore pipeline:
  `go test -tags=e2e ./cmd/kronos`.

## Blocking Work Before Calling The Whole Product Production-Ready

1. Extend E2E coverage into release verification drills and remaining
   service-level failure scenarios.
2. Add deeper verification drill evidence, including failure-injection
   scenarios beyond the current agent-level missing/corrupted chunk drills.
3. Run at least one signed-tag release rehearsal against a disposable version
   tag and archive the verification evidence.

## Next Engineering Slices

1. Configure a local GPG signing key, run `check-release-signing.sh`, push a
   disposable signed tag, verify it with `verify-release-tag.sh`, and archive
   checksum, signature, and attestation verification evidence.

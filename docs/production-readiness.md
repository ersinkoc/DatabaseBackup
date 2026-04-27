# Production Readiness

Last reviewed from the repository state on April 27, 2026.

Kronos is close to production-ready for the implemented Redis or Valkey backup
path with local or S3-compatible storage. A PostgreSQL logical backup/restore
MVP is now present through `pg_dump` and `psql`, with worker/control-plane/local
repository smoke E2E coverage and real-service PostgreSQL conformance running
in CI against PostgreSQL 15, 16, and 17. That conformance now covers
extension-backed data, large objects, restore guardrails, rollback behavior for
failed restores, a 2,500-row indexed JSONB restore rehearsal, and optional
PostgreSQL global role metadata capture through `include_globals=true` without
leaking role password material, plus a focused `postgres_globals` restore drill
for role metadata. CI also runs a PostgreSQL 15-to-17 restore rehearsal. It
still needs larger operator-scale restore drills and broader upgrade
rehearsals before it should be treated as a fully production-grade PostgreSQL
path. The full product vision
across MongoDB, SFTP, Azure Blob, Google Cloud Storage, deeper WebUI workflows,
and multi-instance control-plane operation is still roadmap work. MySQL/MariaDB
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
| Implemented Redis/local/S3 path | 93% | Core pipeline, agent/server flow, lost-agent recovery, server restart recovery, restore planning, retention, audit, metrics, release scripts, Kubernetes examples, runbooks, a reusable production gate, and tagged worker/control-plane/Redis backup, restore, retention apply, and recovery E2E tests are in place. |
| Broad multi-database product vision | 92% | Redis is executable, PostgreSQL now has a plain SQL logical driver MVP, optional global role metadata capture and focused global restore coverage in real-service conformance, worker pipeline smoke E2E coverage, CI conformance coverage across PostgreSQL 15, 16, and 17, and a PostgreSQL 15-to-17 restore rehearsal. MySQL/MariaDB now has a `mysqldump`/`mysql` logical MVP with unit coverage, real-service MySQL 8.4 and MariaDB 11.4 conformance, bidirectional MySQL/MariaDB restore rehearsal coverage, and 10,000-row MySQL/MariaDB restore drills, while MongoDB, storage backends, WebUI workflows, and multi-instance deployment patterns remain roadmap work. |
| Current repository release hygiene | 99% | Tests, vet, format checks, OpenAPI checks, release artifacts, provenance, SBOM metadata, GitHub build/SBOM attestations, keyless cosign signatures and verification, consumer release verification docs, CI govulncheck, release artifact smoke checks, PostgreSQL, MySQL, MariaDB, bidirectional MySQL/MariaDB restore rehearsal conformance, and 10,000-row MySQL/MariaDB restore drills, the production check script, tagged backup/restore/retention/recovery E2E coverage, and Node 24-native GitHub Actions are present. The `golang.org/x/crypto` advisories are fixed. |

## Current Release Gate

Use this command before a release candidate:

```bash
GO=.tools/go/bin/go ./scripts/production-check.sh
```

The gate checks formatting, runs `go vet`, runs the full Go test suite, builds
the binary, validates shell scripts, validates bash completion syntax, and
executes `kronos version`.

## Production-Ready Strengths

- Core streaming backup pipeline with chunking, compression, encryption
  envelopes, signed manifests, restore planning, and verification.
- Redis/Valkey driver coverage with backup and restore paths.
- PostgreSQL logical driver MVP using `pg_dump` for full backups and `psql` for
  restores, with deterministic command-runner unit tests, tagged worker
  pipeline smoke E2E coverage, CI real-service conformance coverage across
  PostgreSQL 15, 16, and 17,
  extension-backed data, large object checks, indexed JSONB bulk restore
  checks, optional
  `pg_dumpall --globals-only --no-role-passwords` role metadata capture with a
  real-service conformance assertion, focused `postgres_globals` restore checks
  for role metadata, `replace_existing` enforcement for non-dry-run restores,
  single-transaction `psql` execution, rollback verification for failed
  restores, and a PostgreSQL 15-to-17 restore rehearsal.
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
- Local and S3-compatible storage backends.
- Persistent control plane state, scheduler state, jobs, backups, retention,
  notifications, users, tokens, and audit log.
- Scoped bearer tokens, role-capped token creation, token lifecycle operations,
  request IDs, security headers, and mutation audit events.
- Health, readiness, metrics, OpenAPI, operations docs, deployment topology
  docs, restore drill docs, release verification docs, release scripts,
  provenance metadata, SBOM metadata, GitHub build/SBOM attestations, keyless
  cosign release signatures and verification, and Kubernetes examples.
- CI runs formatting, vet, staticcheck, govulncheck, race tests, PostgreSQL
  15/16/17 service conformance, PostgreSQL 15-to-17 restore rehearsal, MySQL
  8.4 and MariaDB 11.4 service conformance, bidirectional MySQL/MariaDB
  restore rehearsals, 10,000-row MySQL/MariaDB restore drills, release artifact
  verification, container builds, completion syntax checks, and the
  production-readiness gate. Release
  artifacts are also smoke-tested by
  executing the host binary and validating generated shell completion.
- Tagged E2E coverage exercises a control-plane HTTP server, worker agent,
  local repository storage, and Redis-compatible RESP target together for
  backup and restore. It also covers retention apply over committed backup
  metadata, including dry-run behavior, deletion, and mutation audit recording.
  Lost-agent recovery is covered through heartbeat, claim, failed running job,
  target unblock, and recovery audit behavior. Server restart recovery is
  covered through persisted running/finalizing jobs, boot-time recovery, HTTP
  job inspection, and post-shutdown state verification. PostgreSQL worker smoke
  coverage exercises backup and restore through fake `pg_dump`/`psql` tools,
  the control plane, local storage, manifests, and the restore pipeline:
  `go test -tags=e2e ./cmd/kronos`.

## Blocking Work Before Calling The Whole Product Production-Ready

1. Harden PostgreSQL operational behavior around full-cluster global-object
   restore rehearsals, operator-scale restore drills, and broader upgrade
   rehearsal evidence.
2. Extend E2E coverage into more retention policy edge cases and release
   verification drills.
3. Expand the WebUI from dashboard shell into live resource CRUD, job detail,
   backup detail, and restore workflows.
4. Decide the supported multi-instance story for control-plane state, or
   document single-replica constraints as a hard production boundary.
5. Run at least one signed-tag release rehearsal against a disposable version
   tag and archive the verification evidence.

## Next Engineering Slices

1. Extend PostgreSQL hardening around full-cluster global-object restore
   rehearsals, operator-scale restore drills, and broader upgrade rehearsal
   evidence.
2. WebUI live API wiring for overview, jobs, backups, agents, and readiness.
3. Production deployment hardening for single-replica Kubernetes and external
   secret management.
4. Run a signed-tag release rehearsal and archive checksum, signature, and
   attestation verification evidence.

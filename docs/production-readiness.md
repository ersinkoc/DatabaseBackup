# Production Readiness

Last reviewed from the repository state on April 27, 2026.

Kronos is close to production-ready for the implemented Redis or Valkey backup
path with local or S3-compatible storage. A PostgreSQL logical backup/restore
MVP is now present through `pg_dump` and `psql`, with worker/control-plane/local
repository smoke E2E coverage and real-service PostgreSQL conformance running
in CI. That conformance now covers extension-backed data, large objects, restore
guardrails, and rollback behavior for failed restores. It still needs broader
PostgreSQL operational hardening around role/global-object handling and more
upgrade/version matrices before it should be treated as a fully production-grade
PostgreSQL path. The full product vision
across MySQL,
MongoDB, SFTP, Azure Blob, Google Cloud Storage, deeper WebUI workflows, and
multi-instance control-plane operation is still roadmap work.

## Readiness Estimate

| Scope | Estimate | Notes |
| --- | ---: | --- |
| Implemented Redis/local/S3 path | 93% | Core pipeline, agent/server flow, lost-agent recovery, server restart recovery, restore planning, retention, audit, metrics, release scripts, Kubernetes examples, runbooks, a reusable production gate, and tagged worker/control-plane/Redis backup, restore, retention apply, and recovery E2E tests are in place. |
| Broad multi-database product vision | 80% | Redis is executable, PostgreSQL now has a plain SQL logical driver MVP, worker pipeline smoke E2E coverage, CI real-service conformance coverage for extension-backed data, large objects, restore guardrails, and rollback behavior. MySQL, MongoDB, storage backends, WebUI workflows, and multi-instance deployment patterns remain roadmap work. |
| Current repository release hygiene | 98% | Tests, vet, format checks, OpenAPI checks, release artifacts, provenance, SBOM metadata, GitHub build/SBOM attestations, keyless cosign signatures and verification, CI govulncheck, release artifact smoke checks, PostgreSQL service conformance, the production check script, tagged backup/restore/retention/recovery E2E coverage, and Node 24-native GitHub Actions are present. The `golang.org/x/crypto` advisories are fixed. |

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
  pipeline smoke E2E coverage, CI real-service conformance coverage,
  extension-backed data and large object checks, `replace_existing` enforcement
  for non-dry-run restores, single-transaction `psql` execution, and rollback
  verification for failed restores.
- Local and S3-compatible storage backends.
- Persistent control plane state, scheduler state, jobs, backups, retention,
  notifications, users, tokens, and audit log.
- Scoped bearer tokens, role-capped token creation, token lifecycle operations,
  request IDs, security headers, and mutation audit events.
- Health, readiness, metrics, OpenAPI, operations docs, deployment topology
  docs, restore drill docs, release scripts, provenance metadata, SBOM
  metadata, GitHub build/SBOM attestations, keyless cosign release signatures
  and verification, and Kubernetes examples.
- CI runs formatting, vet, staticcheck, govulncheck, race tests, PostgreSQL
  service conformance, release artifact verification, container builds,
  completion syntax checks, and the production-readiness gate. Release
  artifacts are also smoke-tested by executing the host binary and validating
  generated shell completion.
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

1. Harden PostgreSQL operational behavior around roles, global objects, version
   compatibility, and larger restore drills.
2. Extend E2E coverage into more retention policy edge cases and release
   verification drills.
3. Expand the WebUI from dashboard shell into live resource CRUD, job detail,
   backup detail, and restore workflows.
4. Decide the supported multi-instance story for control-plane state, or
   document single-replica constraints as a hard production boundary.
5. Add consumer-facing attestation verification docs and release verification
   drills.

## Next Engineering Slices

1. Extend PostgreSQL hardening around roles, global objects, version
   compatibility, and larger restore drills.
2. WebUI live API wiring for overview, jobs, backups, agents, and readiness.
3. Production deployment hardening for single-replica Kubernetes and external
   secret management.
4. Add consumer-facing attestation verification docs and release verification
   drills.

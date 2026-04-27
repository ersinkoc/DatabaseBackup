# Production Readiness

Last reviewed from the repository state on April 27, 2026.

Kronos is close to production-ready for the implemented Redis or Valkey backup
path with local or S3-compatible storage. It is not yet production-ready for
the full product vision across PostgreSQL, MySQL, MongoDB, SFTP, Azure Blob,
Google Cloud Storage, deeper WebUI workflows, and multi-instance control-plane
operation.

## Readiness Estimate

| Scope | Estimate | Notes |
| --- | ---: | --- |
| Implemented Redis/local/S3 path | 89% | Core pipeline, agent/server flow, restore planning, retention, audit, metrics, release scripts, Kubernetes examples, runbooks, a reusable production gate, and a tagged worker/control-plane/Redis backup E2E test are in place. |
| Broad multi-database product vision | 67% | The architecture is strong, but major drivers, storage backends, WebUI workflows, and multi-instance deployment patterns remain roadmap work. |
| Current repository release hygiene | 87% | Tests, vet, format checks, OpenAPI checks, release artifacts, provenance, SBOM metadata, CI govulncheck, the production check script, and tagged E2E coverage are present. The `golang.org/x/crypto` advisories are fixed. |

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
- Local and S3-compatible storage backends.
- Persistent control plane state, scheduler state, jobs, backups, retention,
  notifications, users, tokens, and audit log.
- Scoped bearer tokens, role-capped token creation, token lifecycle operations,
  request IDs, security headers, and mutation audit events.
- Health, readiness, metrics, OpenAPI, operations docs, deployment topology
  docs, restore drill docs, release scripts, provenance metadata, SBOM
  metadata, and Kubernetes examples.
- CI runs formatting, vet, staticcheck, govulncheck, race tests, release
  artifact verification, container builds, completion syntax checks, and the
  production-readiness gate.
- Tagged E2E coverage exercises a control-plane HTTP server, worker agent,
  local repository storage, and Redis-compatible RESP target together:
  `go test -tags=e2e ./cmd/kronos`.

## Blocking Work Before Calling The Whole Product Production-Ready

1. Add at least one more first-class database driver, starting with PostgreSQL
   or MySQL, plus backup and restore conformance tests.
2. Extend E2E coverage from backup-only into restore and retention apply flows.
3. Expand the WebUI from dashboard shell into live resource CRUD, job detail,
   backup detail, and restore workflows.
4. Decide the supported multi-instance story for control-plane state, or
   document single-replica constraints as a hard production boundary.
5. Sign or attest release provenance and SBOM metadata in CI.

## Next Engineering Slices

1. Add E2E restore coverage for a previously committed Redis backup.
2. PostgreSQL driver MVP with schema/data backup and restore smoke tests.
3. WebUI live API wiring for overview, jobs, backups, agents, and readiness.
4. Production deployment hardening for single-replica Kubernetes and external
   secret management.

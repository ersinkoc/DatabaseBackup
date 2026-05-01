# Implemented-Path Production Readiness

Last reviewed from the repository state on April 30, 2026.

This track covers what the current repository can honestly support: the Kronos
control plane, CLI, polling agent, local/S3-compatible/SFTP/Azure Blob/GCS
repositories, Redis or Valkey native backups, and PostgreSQL/MySQL/MariaDB/
MongoDB logical backup MVPs that shell out to matching database client tools on
worker agents.

## Ready

- Core backup objects are chunked, compressed, encrypted, hashed, and recorded
  in signed manifests.
- Local filesystem, S3-compatible, SFTP, Azure Blob, and Google Cloud Storage
  backends are implemented and covered by tests.
- Redis/Valkey is the most complete native driver path.
- PostgreSQL, MySQL/MariaDB, and MongoDB have external-tool logical backup and
  restore MVPs with unit tests and real-service CI conformance.
- The control plane persists agents, jobs, backups, schedules, retention
  policies, users, tokens, notification rules, and audit events.
- Request IDs, security headers, scoped bearer tokens, token lifecycle
  operations, token verification rate limiting, TLS/mTLS configuration, and
  optional state secret encryption are implemented.
- Prometheus metrics, health/readiness probes, operations docs, deployment
  topology docs, Kubernetes single-replica examples, systemd units, release
  verification docs, SBOM checks, release smoke checks, and a reusable
  production gate exist.
- CI exercises formatting, vet, staticcheck, govulncheck, unit tests, tagged
  E2E tests, release artifact checks, container builds, and database
  conformance/restore rehearsal jobs.

## Required Operating Boundaries

- Run the control plane as a single replica with one authoritative `state.db`.
- Keep local/no-token mode bound to loopback only.
- Configure TLS for networked control-plane access.
- Configure `server.master_passphrase` before storing sensitive target or
  storage option values in the control-plane state DB.
- Install matching database client tools on every worker agent that handles
  PostgreSQL, MySQL/MariaDB, or MongoDB jobs.
- Treat external-tool driver credentials as host-sensitive because environment
  variables and temporary config files remain observable to sufficiently
  privileged local users.
- Validate restore drills against representative data before promoting a target
  family to production.

## Release Gate

Run before a release candidate:

```bash
GO=.tools/go/bin/go GOFMT=.tools/go/bin/gofmt ./scripts/production-check.sh
.tools/go/bin/go test -tags=e2e ./cmd/kronos
```

Release promotion also requires signed tag verification and archived evidence
for checksums, signatures, SBOM/provenance, and smoke-test output.

## Not Ready In This Track

- HA control-plane operation.
- Automated state DB migration and rollback framework.
- Native PostgreSQL/MySQL/MongoDB protocol drivers.
- PostgreSQL WAL/PITR, MySQL binlog replay, or continuous MongoDB oplog
  streaming as first-class Kronos-managed chains.
- Full browser-auth flow with password login, enforced TOTP, or OIDC.
- Frontend component/security/a11y test suite.
- Broad storage matrix real-service conformance and provider-native identity
  coverage beyond the implemented local, S3-compatible, SFTP, Azure Blob, and
  GCS backends.

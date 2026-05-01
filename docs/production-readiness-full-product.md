# Full-Product Production Readiness

Last reviewed from the repository state on April 30, 2026.

This track covers the long-term product described in `.project/SPECIFICATION.md`.
It is not the current MVP release contract. The full product is not production
ready until the gaps below are implemented and verified.

## Current Verdict

Full-product status: **not production-ready**.

The repository contains a strong implemented-path foundation, but the original
vision includes native database protocols, PITR/incremental recovery, broader
storage support, advanced auth, multi-instance control-plane operation, and
additional API surfaces that are still roadmap work.

## Blocking Gaps

- Native PostgreSQL, MySQL/MariaDB, and MongoDB protocol drivers are absent.
- PostgreSQL WAL/PITR, MySQL binlog replay, and continuous MongoDB oplog
  streaming are not implemented as first-class recovery chains.
- FTP and WebDAV storage backends are not implemented. SFTP has an executable
  backend path and in-process SSH/SFTP conformance, with OpenSSH container
  conformance still required before it is considered complete. Azure Blob has
  an executable backend path and HTTP conformance, with Azurite/service
  conformance and managed identity support still required before it is
  considered complete. GCS has an executable backend path and JSON API HTTP
  conformance, with fake-gcs/service conformance plus service-account/ADC auth
  still required before it is considered complete.
- OIDC, local password login, enforced TOTP, and browser-session hardening are
  incomplete.
- gRPC and MCP API surfaces are not implemented.
- Multi-instance/HA control-plane operation is not supported.
- State DB schema migration, rollback automation, and externalized state
  patterns are not complete.
- Frontend workflow coverage, component tests, and security/a11y tests are not
  complete.

## Evidence Required Before Full-Product Release

- Native driver conformance for each claimed database family.
- PITR or incremental recovery rehearsals for each claimed database family.
- Failure-injection restore evidence for corrupted chunks, missing chunks,
  failed external dependencies, partial writes, and interrupted restores.
- Race-test evidence from an environment with CGO and a working C compiler.
- Load and soak tests for the scheduler, agent concurrency, state store, and
  repository backends.
- HA/upgrade/rollback drills for control-plane state.
- Auth security tests covering browser workflows, token scope denial matrices,
  and secret redaction/persistence behavior.
- Release evidence with signed tags, verified signatures, SBOM/provenance, and
  smoke-tested artifacts.

## Relationship To The Implemented Track

The implemented track can be piloted under narrow operating boundaries. The
full-product track should remain **no-go** until the blocking gaps above are
closed and backed by repeatable CI or release evidence.

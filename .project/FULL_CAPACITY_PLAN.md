# Kronos Full Capacity Plan

Last updated: April 30, 2026.

This is the execution plan for the complete product, not a reduced release
track. Every item below must be implemented, wired into CLI/API/WebUI where
applicable, and backed by repeatable verification before Kronos is called full
capacity.

## Capacity Gates

1. **Storage complete**: local, S3-compatible, SFTP, Azure Blob, and Google
   Cloud Storage support the full object backend contract: put, get, range,
   head, exists, delete, list, retries, auth modes, and conformance tests.
2. **Database complete**: PostgreSQL, MySQL/MariaDB, MongoDB, and Redis/Valkey
   have native snapshot, restore, and change-stream recovery paths.
3. **Point-in-time complete**: WAL, binlog, oplog, and Redis command/AOF stream
   capture can restore to timestamp or native position.
4. **Auth complete**: password login, enforced admin TOTP, OIDC, scoped API
   tokens, mTLS agents, browser session hardening, and denial-matrix tests.
5. **Operations complete**: HA or explicitly coordinated control-plane mode,
   state migration/rollback, restore drills, failure injection, load tests,
   release evidence, dashboards, and runbooks.
6. **Experience complete**: CLI, REST/OpenAPI, WebUI, gRPC, and MCP expose the
   same supported capability set without dead screens or undocumented traps.

## Workstream A: Storage Backends

- **A1 SFTP backend**: SFTP object backend, SSH password/key/agent auth,
  host-key verification, CLI options, agent factory wiring, unit tests, and
  in-process SSH/SFTP conformance are implemented. Remaining work: OpenSSH
  container conformance and operations docs.
- **A2 Azure Blob backend**: block blob upload, full/range reads, metadata,
  delete, listing pagination, SAS, Shared Key signing, CLI options, agent
  factory wiring, and HTTP conformance are implemented. Remaining work:
  Azurite/service conformance, retry hardening, and managed identity token
  support.
- **A3 GCS backend**: JSON API media upload, full/range reads, metadata,
  delete, listing pagination, bearer token/API key options, CLI options, agent
  factory wiring, and HTTP conformance are implemented. Remaining work:
  fake-gcs/service conformance, retry hardening, service-account JWT, and ADC.
- **A4 Storage conformance**: common backend conformance suite covering
  put/get/head/exists/delete/list, ranges, conflicts, missing objects, size
  mismatches, invalid keys, and canceled contexts is implemented for local,
  S3, SFTP, Azure Blob, GCS, and the memory test backend. Remaining work:
  larger stress objects, transient network failures, and provider emulator
  matrices.

## Workstream B: Native Database Drivers

- **B1 PostgreSQL native snapshot**: pgwire startup, TCP dialing, SSLRequest/TLS
  negotiation, cleartext-password auth, MD5 auth, SCRAM-SHA-256 auth, simple
  query framing, RowDescription/DataRow/CommandComplete/ReadyForQuery, error
  parsing, catalog extension/enum/domain/sequence/table/column/constraint/index/
  view/routine/trigger discovery, and a native plain-SQL snapshot path plus
  native plain-SQL restore execution are implemented as the native foundation.
  Remaining work: extended query, richer schema objects such as composite types
  and operators, materialized-view data refresh, COPY binary export/import,
  globals, cancellation cleanup, and version matrix.
- **B2 PostgreSQL PITR**: replication slots, BASE_BACKUP or logical stream
  capture, WAL segment persistence, restore-to-LSN and restore-to-timestamp.
- **B3 MySQL/MariaDB native snapshot**: handshake/auth, result decoding, schema
  capture, consistent snapshot, data export/import, version matrix.
- **B4 MySQL/MariaDB PITR**: GTID-aware binlog stream capture, row event
  parser, replay, restore-to-GTID and restore-to-timestamp.
- **B5 MongoDB native snapshot**: OP_MSG, BSON coverage, SCRAM auth, database,
  collection, index, and document streaming.
- **B6 MongoDB PITR**: replica-set snapshot point, continuous oplog capture,
  replay, restore-to-timestamp, sharded-cluster discovery.
- **B7 Redis/Valkey stream path**: PSYNC/RDB parser, command stream capture,
  ACL/functions/modules guardrails, restore-to-offset/time semantics.

## Workstream C: Recovery Correctness

- Parent-chain validation for every backup type.
- Restore rehearsals with representative large datasets per database family.
- Failure-injection drills for missing chunks, corrupt chunks, bad manifests,
  interrupted restores, dropped network sessions, and agent/server restarts.
- Differential verification that restored databases match source structure,
  row/document/key counts, checksums, grants, indexes, and selected binary data.

## Workstream D: Auth, Identity, And API Surface

- Local password login with bcrypt/argon2id password storage.
- Mandatory TOTP for admin roles and recovery-code flow.
- OIDC provider support with tested Keycloak plus documented provider mapping.
- Browser session cookies with CSRF protection and secure defaults.
- gRPC admin/agent surface or a formally equivalent hardened transport.
- MCP tools with approval-gated mutating actions and audit records.

## Workstream E: HA, State, And Operations

- State schema migration framework with forward/backward rehearsal tests.
- Automated state DB backup/restore and rollback tooling.
- Coordinated multi-instance or documented active/passive control-plane mode.
- Scheduler and job-claim load tests at production scale.
- Long-running soak tests for agents, storage backends, and state stores.
- Release workflow requiring signed tags, signatures, SBOM/provenance, smoke
  tests, vulnerability checks, and archived evidence.

## Workstream F: WebUI And Developer Experience

- WebUI support for every resource and recovery workflow: targets, storage,
  schedules, backups, PITR streams, restore drills, users, OIDC, tokens,
  notifications, keys, agents, and audit.
- Component, API-client, Playwright, and accessibility tests.
- CLI parity for all supported operations.
- Documentation generated from the same supported capability matrix.

## Immediate Build Order

1. Finish SFTP/OpenSSH, Azure/Azurite, and GCS/fake-gcs real-service conformance
   plus docs.
2. Add stress/failure-injection coverage to the common storage backend suite.
3. Start PostgreSQL native snapshot and WAL/PITR in parallel with auth work.
4. Follow with MySQL binlog and MongoDB oplog recovery.
5. Close auth/browser/gRPC/MCP and HA/state migration after core recovery paths
   are testable end to end.

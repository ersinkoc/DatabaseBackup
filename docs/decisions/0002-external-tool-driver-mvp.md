# 0002: External Tool Driver MVP

Status: accepted for MVP, conflicts with original specification

Kronos currently treats PostgreSQL, MySQL/MariaDB, and MongoDB support as an
external-tool MVP rather than a native wire-protocol implementation:

- PostgreSQL uses `pg_dump`, `pg_dumpall`, and `psql`.
- MySQL/MariaDB uses `mysqldump`/`mariadb-dump` and `mysql`/`mariadb`.
- MongoDB uses `mongodump` and `mongorestore`.
- Redis/Valkey remains the most complete native executable driver in this
  build.

This is an explicit product decision for the current implementation, not an
accidental dependency. It contradicts `.project/SPECIFICATION.md` and
`.project/PROMPT.md`, which require pure Go database protocol drivers and
forbid shelling out. Those documents remain useful as the long-term product
vision, but they are no longer accurate release criteria for the current MVP.

## Rationale

The external-tool path provides usable backup/restore coverage, real-service CI
conformance, and faster restore-drill feedback while the native protocol work is
still too large for the current release window. The tradeoff is real:

- Worker agents must have matching database client tools installed.
- Runtime behavior depends on tool versions and platform packaging.
- Incremental/PITR support remains absent for PostgreSQL/MySQL/MongoDB.
- The original "single zero-dependency binary" claim is false for those
  database paths.

## Security Requirements

Tool-wrapper drivers must not place database passwords in process-visible
command arguments:

- PostgreSQL passes passwords through `PGPASSWORD` and strips password material
  from `--dbname` URIs.
- MySQL/MariaDB passes passwords through `MYSQL_PWD`.
- MongoDB writes a per-command 0600 temporary YAML file and passes only the
  `--config` path to `mongodump`/`mongorestore`, matching MongoDB Database
  Tools guidance for avoiding process-list password exposure.

This reduces command-line exposure but does not make the tool-wrapper approach
equivalent to native drivers. Environment variables, temporary files, local
process privileges, and persisted target credentials still require host-level
hardening.

## Decision Gate

For v0.1/MVP:

- Document PostgreSQL/MySQL/MongoDB as external-tool drivers.
- Keep native PostgreSQL/MySQL/MongoDB protocol work on the roadmap.
- Do not market the implemented product as "no `pg_dump`/`mysqldump`/
  `mongodump` required."
- Require restore drills and conformance tests for every supported tool path.

For v1.0:

- Either implement native protocol drivers for the database families claimed as
  zero-dependency, or permanently revise the specification and branding around
  supported external tools.

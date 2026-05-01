# Kronos — SPECIFICATION

> Current implementation note, April 30, 2026: this document preserves the
> long-term product vision. It is not an accurate MVP release contract where it
> claims pure-Go database drivers, no shell-outs, PITR for PostgreSQL/MySQL/
> MongoDB or gRPC agent transport. The implemented build uses
> external database tools for PostgreSQL, MySQL/MariaDB, and MongoDB, hardened
> HTTP polling for agents, and local/S3/SFTP/Azure Blob/GCS storage. See
> [`docs/decisions/0002-external-tool-driver-mvp.md`](../docs/decisions/0002-external-tool-driver-mvp.md)
> and [`docs/production-readiness.md`](../docs/production-readiness.md) for the
> current release scope.

> **Kronos** is the Titan of Time. Time devours obsolete backups; Kronos preserves what matters.
>
> A zero-dependency, single-binary database backup manager written in pure Go.
> Agent/Server architecture. Core 4 database support (PostgreSQL, MySQL/MariaDB, MongoDB, Redis).
> Multi-backend storage, client-side encryption, cron-based scheduling, PITR, WebUI, CLI, MCP, REST+gRPC.

---

## 1. Vision & Scope

### 1.1 Problem Statement
Every organisation running databases eventually faces the same set of operational problems:

- **Tool sprawl** — `pg_dump` for Postgres, `mysqldump` for MySQL, `mongodump` for Mongo, `redis-cli --rdb` for Redis, plus custom shell scripts to glue them to S3 with encryption.
- **Silent failures** — cron jobs that return exit code 0 but produced a 0-byte file; nobody notices until the restore.
- **No PITR** — most ad-hoc setups do nightly full dumps and lose up to 24 hours of data on disaster.
- **Restore theatre** — backups are rarely test-restored; the first real restore is during a production incident.
- **Secret sprawl** — S3 keys, encryption passphrases and DB credentials scattered across `crontab`, `.env`, and CI secrets.
- **Opaque retention** — "we keep 30 days" actually means 30 files regardless of size, or unbounded growth.

### 1.2 Kronos in One Sentence
A single Go binary that operators run as `kronos-server` (control plane) and `kronos-agent` (co-located with each database) to get scheduled, encrypted, compressed, deduplicated, verified backups with point-in-time recovery, automated retention (GFS), multi-backend storage, and a modern WebUI — with no external dependencies, no database of its own, and no `pg_dump` installation required on target hosts.

### 1.3 Non-Goals (explicit)
- **Not** a general-purpose file/folder backup tool (use Restic or BorgBackup for that).
- **Not** a VM/host-image backup tool (use Velero, Veeam).
- **Not** a replication tool or cluster manager — Kronos backs data up, it does not keep replicas in sync live.
- **Not** a SaaS — ships as a binary; if you want managed, host it yourself.
- **No Kafka, no RabbitMQ, no external coordination** — stdlib + embedded storage only.

### 1.4 Target Users & Personas
| Persona | Scale | Pain Kronos Solves |
|---------|-------|---------------------|
| **Solo developer / founder** | 1–3 DBs, single VPS | Replaces the `0 3 * * * pg_dump | gzip | aws s3 cp` spaghetti |
| **SRE at growth startup** | 10–50 DBs across environments | Centralised policy, audit, alerting; enforces encryption; test-restore automation |
| **Platform engineer at mid-market** | 50–500 DBs, multi-region, multi-cloud | RBAC, secret mgmt, retention compliance, agent fleet management, Prometheus/Otel integration |
| **Self-hosted enthusiast** | Home lab / small ops | Single-binary simplicity; no agent required for local DBs; WebUI |

---

## 2. Architecture Overview (Conceptual)

```
                          +------------------------+
                          |     CLI / WebUI /      |
                          |   MCP / REST / gRPC    |
                          +-----------+------------+
                                      |
                                      v
+-------------------+        +------------------+        +-----------------+
|  Secret Backends  |<-------|  kronos-server   |------->|  Notification   |
|  (age / vault /   |        |  (control plane) |        |  Channels       |
|   env / file)     |        +--------+---------+        +-----------------+
+-------------------+                 |
                                      | gRPC + mTLS (port 8500 default)
                                      |
      +-------------------------------+-------------------------------+
      |                               |                               |
      v                               v                               v
+------------+                  +------------+                   +------------+
| kronos-    |                  | kronos-    |                   | kronos-    |
| agent #1   |                  | agent #2   |     . . . . .     | agent #N   |
| (near PG)  |                  | (near MY)  |                   | (near MO)  |
+-----+------+                  +-----+------+                   +-----+------+
      |                               |                                |
      v                               v                                v
+------------+                  +------------+                   +------------+
| PostgreSQL |                  | MySQL /    |                   | MongoDB    |
| (TCP 5432) |                  | MariaDB    |                   | (27017)    |
+------------+                  +------------+                   +------------+

Server ----> Storage Backends (S3 / Local / SFTP / Azure Blob / GCS / WebDAV)
         (all traffic originates from the agent; server only orchestrates)
```

### 2.1 Binary Topology
Three binaries produced from one Go module:

1. **`kronos-server`** — Control plane. Holds configuration, scheduler, WebUI, REST+gRPC+MCP APIs, audit log, orchestration state. Single embedded storage (BoltDB-compatible pure-Go B+Tree) for scheduler state, job history, audit.
2. **`kronos-agent`** — Runs on (or near) a database host. Executes backup/restore operations. Streams artefacts directly to storage (never relayed through server). Stateless — can be killed and restarted.
3. **`kronos`** — Unified CLI. Talks to server via gRPC (admin ops) or directly to a local agent (`kronos backup now --local`) for emergency single-host use.

All three share the same codebase; the same binary can be started in either mode via `kronos server|agent|cli`. For distribution we ship one `kronos` binary plus convenience symlinks.

### 2.2 Local / Embedded Mode
A single `kronos local` invocation runs server + agent in the same process for home-lab / solo scenarios. No mTLS, no auth (localhost only), WebUI on `127.0.0.1:8500`. This is the **on-ramp** that competes with a cron+shell setup.

---

## 3. Functional Requirements

### 3.1 Database Drivers (Core 4)

All drivers MUST be implemented in pure Go against the database wire protocol. **No shelling out to `pg_dump`, `mysqldump`, or `mongodump` is permitted.** This is a hard non-negotiable constraint of the project.

#### 3.1.1 PostgreSQL Driver
- Wire protocol: PostgreSQL v3 (supports 10, 11, 12, 13, 14, 15, 16, 17).
- Logical backup: `COPY TO STDOUT` for table data, system catalog queries for schema, per-table parallel.
- Physical backup: streaming base backup via replication protocol (`BASE_BACKUP` command).
- WAL streaming: continuous `START_REPLICATION` for PITR.
- Restore: `COPY FROM STDIN` for logical, file extraction + `pg_resetwal`-equivalent for physical.
- Schema-only / data-only / table-filtered backups.
- Exclude/include patterns by schema/table (glob).
- Consistent snapshot via transaction isolation (`BEGIN ISOLATION LEVEL REPEATABLE READ; pg_export_snapshot();` equivalent pattern).
- TLS/SSL support with CA verification, SCRAM-SHA-256, MD5, and password auth.

#### 3.1.2 MySQL / MariaDB Driver
- Wire protocol: MySQL client/server protocol (compatible with MySQL 5.7, 8.0, 8.4, MariaDB 10.6+, 11.x).
- Logical backup: `SHOW CREATE TABLE` + chunked `SELECT ... INTO OUTFILE` equivalents via protocol.
- Binary log streaming: `COM_BINLOG_DUMP_GTID` for PITR (GTID-aware).
- Physical backup: mysql.ibd / binary copy mode (advanced, phase 2 for physical).
- Consistent snapshot via `FLUSH TABLES WITH READ LOCK` + `START TRANSACTION WITH CONSISTENT SNAPSHOT` (InnoDB).
- Parallel per-table dump with chunking on primary-key ranges.
- Restore replays DDL then data; binlog replay up to target GTID/timestamp.
- TLS, caching_sha2_password, mysql_native_password, ed25519 (MariaDB).

#### 3.1.3 MongoDB Driver
- Wire protocol: MongoDB Wire Protocol (OP_MSG, compatible with MongoDB 4.4, 5.0, 6.0, 7.0, 8.0).
- Logical backup: `find` with cursor iteration per collection, `listIndexes`, `listCollections`.
- Oplog streaming: tailable cursor on `local.oplog.rs` for PITR (replica-set mode only).
- Consistent snapshot via causal consistency + `atClusterTime` when the deployment is a replica set / sharded cluster.
- BSON preserved end-to-end (no JSON conversion loss).
- Restore via `insert` batches, indexes rebuilt at the end.
- SCRAM-SHA-1, SCRAM-SHA-256 auth. TLS. X.509 client cert auth.

#### 3.1.4 Redis Driver
- Wire protocol: RESP2 + RESP3 (compatible with Redis 6, 7, 8; Valkey 7.2+; KeyDB; Dragonfly; Garnet partial).
- Logical backup: `SCAN` + per-key type-aware export (`DUMP` opcode gives portable binary form).
- RDB streaming: `REPLICAOF` + parse RDB bytes incrementally (no disk intermediate on source).
- AOF streaming: `REPLICAOF` mode captures replication stream for PITR.
- Consistent snapshot via `BGSAVE` trigger + RDB stream capture.
- Restore via `RESTORE` with TTL preservation.
- Password auth, ACL users (Redis 6+), TLS.

### 3.2 Backup Types

| Type | Kronos Name | Supported By | Description |
|------|-------------|--------------|-------------|
| Full | `full` | All 4 | Complete logical or physical snapshot. |
| Incremental (by chunk dedup) | `incr` | All 4 | Only changed content-defined chunks since previous backup are uploaded. |
| Differential | `diff` | All 4 | All changes since the most recent full backup. |
| PITR (WAL/binlog/oplog) | `stream` | PG, MySQL, Mongo, Redis (AOF) | Continuous stream of change records. |
| Schema-only | `schema` | PG, MySQL, Mongo | DDL only, no data. |

**Chain semantics**: Every backup references its parent via content-addressed chunk manifests. Kronos refuses to delete a full backup whose descendants still exist; retention policy walks the chain.

### 3.3 Storage Backends

Every backend implements the same `storage.Backend` interface. All streaming, not load-into-memory.

| Backend | Phase 1 | Auth |
|---------|---------|------|
| Local filesystem | ✅ | POSIX |
| S3 (AWS + compatible) | ✅ | Static key, IAM role, STS, IMDSv2, EC2 instance profile, IRSA (EKS) |
| MinIO / R2 / B2 / Wasabi / Ceph RGW | ✅ | S3-compatible path |
| SFTP | ✅ | Password, key, ssh-agent |
| FTP/FTPS | ✅ | Password |
| Azure Blob | ✅ | SAS, shared key, managed identity |
| Google Cloud Storage | ✅ | Service account JSON, ADC, workload identity |
| WebDAV | Phase 2 | Basic, Digest |

**S3 implementation is against the wire API directly** (Signature V4, multipart upload, XML responses) — no `aws-sdk-go`. We bring in our own tiny signer because AWS SDK is the single biggest dependency in most Go backup tools.

### 3.4 Compression

| Algorithm | Library | Use |
|-----------|---------|-----|
| zstd | `github.com/klauspost/compress/zstd` (pure-Go) | **Default.** Best size/speed ratio. Dictionary training enabled. |
| gzip | stdlib | Compatibility fallback. |
| lz4 | Pure Go reimpl (vendored or own) | Speed-optimised for large streams. |
| xz (decompress only) | Pure Go | Restoring legacy backups. |

**Exception to #NOFORKANYMORE**: `klauspost/compress` is whitelisted. It is the de-facto pure-Go standard, zero-dep itself, and reimplementing zstd is out of scope.

Adaptive compression: based on first 1 MB entropy, Kronos may automatically switch a backup from zstd-3 to zstd-18 or decide to skip compression for already-compressed binary streams (JPEG blobs in a CMS, etc).

### 3.5 Encryption

- Default: **AES-256-GCM** with 96-bit random nonce per chunk.
- Alternative: **ChaCha20-Poly1305** (preferred on ARM / systems without AES-NI).
- Key derivation: **Argon2id** (64 MiB, 3 iterations, 4 threads) from passphrase, or direct X25519 key exchange for asymmetric repositories (age-style).
- Key hierarchy: passphrase → master key → per-backup data encryption key (DEK) → per-chunk DEK.
- Key rotation: re-encrypts only metadata/manifests, not chunks (chunks remain under their original DEK, manifests point to the new root).
- Key escrow: multiple passphrase unlock slots (like LUKS) so rotating an operator doesn't require re-encrypting the repository.

All crypto is in `crypto/aes`, `crypto/cipher`, `golang.org/x/crypto/chacha20poly1305`, `golang.org/x/crypto/argon2`, `golang.org/x/crypto/curve25519` — no third-party crypto.

### 3.6 Deduplication

- **FastCDC** content-defined chunking with configurable min=512KiB, avg=2MiB, max=8MiB.
- **BLAKE3** hash for chunk identity (64-byte digest, truncated to 32 bytes in manifests).
- Chunks stored under `data/<aa>/<bb>/<hash>` in the backend (two-level sharding).
- Chunk index cached locally on agent for fast uploads (Bloom filter + in-memory map for hot set).
- Repository-wide dedup: chunks shared across databases and backups automatically.
- Garbage collection: mark-and-sweep driven from manifest roots; scheduled weekly by default.

BLAKE3: pure-Go implementation vendored (`lukechampine.com/blake3` is canonical; we either vendor or re-implement — decision logged in IMPLEMENTATION.md).

### 3.7 Scheduling

- **Cron expressions** with 5- and 6-field support (second-precision opt-in).
- **Calendar DSL** (friendly form): `@hourly`, `@daily`, `@weekly`, `@monthly`, `@every 15m`, `@between 02:00-04:00 UTC random` (the last picks a random start time inside a window — useful for load-spreading across an agent fleet).
- **One-off** jobs: `kronos backup now --target prod-pg`.
- **Event-triggered** jobs: webhook endpoint `/v1/triggers/<slug>` kicks a named job with optional payload passed to hooks.
- **Chain triggers**: "after prod-pg full succeeds, start prod-pg-to-analytics restore-into-dev" — specified declaratively.
- Concurrency control: per-target max concurrent jobs, per-agent max concurrent jobs, global queue. Default: one job per target at a time; newer scheduled runs wait.
- Catch-up policy per job: `skip`, `queue`, `run_once` (if multiple missed runs, execute once).
- Jitter: optional random delay 0-N seconds to avoid thundering-herd against shared storage.

### 3.8 Retention (GFS + Count + Size + Time)

Retention is a **policy object** attached to a backup target. A policy may combine rules; the effective keep-set is the union.

- **GFS (Grandfather-Father-Son)**: `daily=7, weekly=4, monthly=12, yearly=3` — tags each backup with the "highest" role it fills.
- **Count-based**: keep last N of a given type.
- **Time-based**: keep everything younger than T.
- **Size-capped**: if repository exceeds X GB, evict oldest non-protected first.
- **Manual protection**: `kronos backup protect <id>` sets a "legal hold" flag that retention ignores.

Deletion walks the chunk reference graph; a chunk is only removed when no surviving manifest references it.

### 3.9 Restore

- **Full restore**: reconstruct the chosen full backup into the target database.
- **Point-in-time restore**: full + differentials/incrementals + streamed WAL/binlog/oplog replayed to a specific timestamp or LSN/GTID/opcode.
- **Table/collection-level restore**: extract just one object without restoring the whole dataset (logical backups only).
- **Restore to different database**: clone prod → staging with configurable rename rules.
- **Verification restore**: automated periodic restore to a throwaway sandbox DB (`--sandbox docker://postgres:16-alpine` or a preconfigured target), checksum validation, then teardown. **This is the feature that separates Kronos from everyone else.**
- **Dry-run**: walk the restore plan, print steps, touch nothing.

### 3.10 Verification & Integrity

Four levels, increasing in cost:

1. **Manifest check** (cheap, every run): backup manifest signature verified, referenced chunks exist in storage (HEAD requests).
2. **Chunk integrity** (medium, scheduled): each chunk's BLAKE3 recomputed and checked against the manifest. Configurable sample rate for very large repos.
3. **Logical replay** (expensive, weekly/monthly): streaming restore into `/dev/null` — decrypt, decompress, parse logical records, throw them away. Catches "the backup is valid bytes but unusable" failures.
4. **Live restore** (most expensive, monthly): restore into a sandbox DB, run configurable SQL queries to validate (e.g. `SELECT COUNT(*) FROM users`), compare against known-good delta.

Each verification level emits metrics and can trigger notifications on failure.

### 3.11 Hooks

- Phases: `pre-backup`, `post-backup`, `on-failure`, `pre-restore`, `post-restore`, `pre-retention`, `post-retention`.
- Executors:
  - **Shell script** (agent-side, sandboxed env, timeout enforced).
  - **HTTP webhook** (server or agent).
  - **Native Go plugin** (loaded via Go plugin system — build tag gated, default off).
  - **WASM plugin** (via `wazero`, optional build tag — Kronos WASI runtime spec in IMPLEMENTATION.md).
- Hook context passed as JSON via stdin / request body: target name, backup ID, size, duration, chunk count, error message, correlation ID.
- Hooks may fail the whole job (fail-closed) or log-and-continue (fail-open) based on config.

### 3.12 Notifications

Channels:
- **Slack** (Incoming Webhook or Bot Token)
- **Discord** (Webhook)
- **Email** (SMTP, STARTTLS, SMTPS; connection pool)
- **Telegram** (Bot API)
- **Webhook** (generic JSON POST with HMAC-SHA256 signing)
- **PagerDuty** (Events API v2)
- **Opsgenie** (API v2)
- **Microsoft Teams** (Incoming Webhook)

Event routing rules:
```yaml
notifications:
  - when: job.failed
    channels: [slack-sre, pagerduty]
  - when: job.succeeded AND target.tier == "tier0"
    channels: [slack-sre]
  - when: retention.deleted_backup
    channels: [slack-audit]
```

### 3.13 Multi-tenancy & RBAC

Three roles out of the box:
- **admin** — full control, can manage users, rotate keys, change storage backends.
- **operator** — can trigger backups/restores, cannot change storage config or manage users.
- **viewer** — read-only; sees dashboards and history.

Scope: roles are granted per **project**. A project is a namespace around backup targets, storage config, schedules, notifications. Users may belong to many projects.

Auth backends:
- Local users (bcrypt passwords, TOTP 2FA mandatory for admin).
- OIDC (generic, tested against Google, Keycloak, Authentik, Auth0, GitHub).
- mTLS client certs (primarily for agent→server and CLI→server).
- API tokens (scoped, expiring, revocable).

### 3.14 Secrets Management

Credentials are never stored as plaintext on disk.

- **Built-in**: age-encrypted secrets file (`secrets.age`) unlocked by the server at startup via a passphrase supplied by env var, file, or interactive prompt.
- **External**: HashiCorp Vault (KV v2, AppRole auth), AWS Secrets Manager, GCP Secret Manager, Azure Key Vault, Doppler, 1Password Connect.
- **Env substitution**: `${env:PG_PASSWORD}` / `${file:/run/secrets/pg}` / `${vault:secret/prod/pg#password}` template syntax inside YAML config.
- **Secret rotation**: on scheduled rotation, Kronos re-reads secrets on next job run without restart.

### 3.15 Observability

- **Metrics** (Prometheus, `/metrics` exposition): job duration, bytes in/out, chunk dedup ratio, backend latencies, scheduler lag, agent health, repository size.
- **Tracing** (OpenTelemetry, OTLP/gRPC): end-to-end spans from `job.scheduled` → `job.started` → per-driver spans → `job.finished`.
- **Structured logs** (JSON, `slog`): correlation ID per job, redacted secrets.
- **Audit log**: append-only, hash-chained, tamper-evident (each line embeds a BLAKE3 of the previous line). Exportable.

### 3.16 Interfaces

#### CLI
```
kronos server                      # run control plane
kronos agent                       # run agent
kronos local                       # run server+agent in one process (home-lab)

kronos target add <name> ...       # declare a backup target
kronos target list
kronos target test <name>          # connectivity + permissions check

kronos storage add <name> ...      # declare a storage backend
kronos storage test <name>
kronos storage du                  # repo usage, per target

kronos schedule add <name> ...
kronos schedule list
kronos schedule pause/resume <name>

kronos backup now --target <t> [--type full|incr|diff] [--tag release-v2]
kronos backup list [--target <t>] [--since 7d]
kronos backup inspect <id>
kronos backup protect <id> / unprotect <id>
kronos backup verify <id> [--level chunk|replay|live]

kronos restore <backup-id> [--to <target>] [--at <timestamp|lsn|gtid>]
                           [--dry-run] [--sandbox docker://...]
                           [--tables schema.t1,schema.t2]

kronos retention apply <target>    # manual retention trigger
kronos retention plan <target>     # show what would be deleted
kronos gc                          # mark-and-sweep orphan chunks

kronos key rotate
kronos key add-slot / remove-slot <id>
kronos key escrow export <file>

kronos user add/list/remove/grant
kronos token create --scope ...

kronos audit tail / search
kronos version
kronos completion bash|zsh|fish
```

All commands have `--output json|yaml|table` and `--server <url>` overrides.

#### WebUI
Single-page app served from `/`. Routes:
- `/` Dashboard: recent jobs, repo size over time, failing targets, next scheduled runs.
- `/targets` Target list, detail view with backup history and schedule.
- `/backups` Global backup explorer with filter/search, size, type, chain visualisation.
- `/schedules` Schedule list, calendar heatmap.
- `/storage` Repo health, per-backend stats, GC status.
- `/restore` Wizard: pick backup → pick target → preview → execute.
- `/audit` Paginated audit log.
- `/settings` Users, roles, tokens, OIDC, notifications, keys.

Served from `embed.FS`. Stack: **Vite 6 + React 19 + TypeScript + Tailwind CSS v4.1 + shadcn/ui + lucide-react**, with TanStack Router, TanStack Query, Zustand, react-hook-form + Zod, and Recharts. Dark / light / system theme, responsive from 360 px phones to 1920 px desktops. Node is a build-time dependency only; the shipped binary contains no JS runtime — see IMPLEMENTATION.md §17.

#### REST API (`/api/v1/...`)
OpenAPI 3.1 spec generated from handler code. Bearer token or mTLS. JSON only. Cursor-based pagination.

Sample endpoints:
```
GET    /api/v1/targets
POST   /api/v1/targets
GET    /api/v1/targets/{name}
POST   /api/v1/targets/{name}/backup            # start one-off
GET    /api/v1/backups?target=...&since=...
GET    /api/v1/backups/{id}
POST   /api/v1/backups/{id}/verify
POST   /api/v1/restore
GET    /api/v1/schedules
POST   /api/v1/schedules
GET    /api/v1/metrics/usage
GET    /api/v1/audit
POST   /api/v1/auth/login
POST   /api/v1/auth/refresh
```

#### gRPC API
Used by agent↔server and high-throughput CLI operations (streaming backup/restore progress). `.proto` files in `api/proto/`. Services: `ControlPlane`, `AgentControl`, `JobStream`.

#### MCP Server
Runs inside `kronos-server`. Default port 8501. Exposes tools to LLMs:

| Tool | Description |
|------|-------------|
| `kronos_list_targets` | List targets with current status. |
| `kronos_list_backups` | Filter by target, time range, type. |
| `kronos_inspect_backup` | Full metadata for one backup. |
| `kronos_backup_now` | Start a backup. |
| `kronos_restore_preview` | Build and return a restore plan (does not execute). |
| `kronos_repo_usage` | Storage usage summary. |
| `kronos_failing_jobs` | Jobs that failed in last N hours. |
| `kronos_next_runs` | Upcoming scheduled runs. |

Mutating tools (`backup_now`, restore execution) require an `approved_by` argument in the session context; absent approval they return a plan only.

---

## 4. Non-Functional Requirements

### 4.1 Performance Targets

| Metric | Target | Measured How |
|--------|--------|--------------|
| Logical backup throughput, PG, 1 GB table, no compression | ≥ 400 MB/s on modern NVMe | Benchmark against sample datasets in `bench/` |
| Incremental backup of unchanged 100 GB DB | ≤ 30s (metadata-only) | End-to-end timer |
| Chunk dedup lookup | ≤ 50 µs p99 | In-process benchmark |
| Restore, 1 GB logical dump | ≥ 300 MB/s | Benchmark |
| Scheduler tick | ≤ 5 ms overhead for 10k schedules | Load test |
| Agent memory @ idle | ≤ 40 MB RSS | `ps` |
| Agent memory @ 1 GiB/s throughput | ≤ 512 MB RSS | `ps` during bench |
| Server memory @ 10k tracked backups | ≤ 256 MB RSS | `ps` |
| Startup time (cold) | ≤ 300 ms | `time ./kronos-server` |

### 4.2 Reliability
- **Durability**: every upload is verified by re-reading (or HEAD + ETag) before the manifest is finalised.
- **Atomic commit**: a backup is only visible in the repository once its manifest is written to a committed path; partial uploads go to `staging/<uuid>/` and are GC'd after 24h.
- **Crash safety**: if the agent crashes mid-backup, the next run resumes chunk upload from the last-acknowledged chunk (staging area is content-addressed, so re-upload of duplicates is a no-op).
- **Server state**: all persistent state is in a single embedded pure-Go B+Tree file (`kronos.db`), fsync'd on write. Backup the `kronos.db` itself to a different backend daily; `kronos self-backup` is a first-class command.

### 4.3 Security
- No plaintext secrets on disk at rest.
- All inter-process network traffic encrypted (mTLS for gRPC, TLS for REST).
- Client-side encryption: storage backends only ever see opaque ciphertext.
- Audit log is hash-chained; tampering detectable.
- Secret redaction in logs (regex-driven, extensible).
- Passwordless bootstrap is refused; first admin is created via one-time token printed on first start.
- Supply-chain: binaries are reproducible, checksummed, signed (cosign keyless via GitHub Actions).

### 4.4 Portability
- Tier 1 (CI-tested each PR): `linux/amd64`, `linux/arm64`, `darwin/amd64`, `darwin/arm64`.
- Tier 2 (release-tested): `windows/amd64`, `freebsd/amd64`.
- Tier 3 (best-effort): `linux/armv7`, `linux/riscv64`.
- No CGo anywhere. `CGO_ENABLED=0` in the release Dockerfile.
- Single static binary, no runtime dependencies.

### 4.5 Deployability
- systemd unit files shipped in `contrib/systemd/`.
- OCI image (distroless-based, `gcr.io/distroless/static-debian12`).
- Helm chart in `contrib/helm/kronos/`.
- Kubernetes operator (Phase 2; lets you declare `Target` / `Schedule` as CRDs).
- Ansible role in `contrib/ansible/`.
- Homebrew tap + Scoop bucket.

### 4.6 Upgrade & Compatibility
- Repository format: versioned (`repo_version: 1`). Kronos refuses to write to a repo version it does not understand and refuses to downgrade.
- Wire protocol between server and agent: versioned; N-1 compatibility guaranteed (agent of version 1.2 talks to server 1.3).
- Config file: JSON Schema provided; `kronos config validate` validates before load.

### 4.7 Dependency Policy (#NOFORKANYMORE)
Allowed module imports:
- Go standard library.
- `golang.org/x/*` (crypto, sys, net, term, sync, text).
- `gopkg.in/yaml.v3`.
- `github.com/klauspost/compress` (zstd only, vendored).
- `lukechampine.com/blake3` (BLAKE3 hash, vendored).

**No** ORMs, no `gorm`, no `sqlx`, no `cobra`, no `spf13/viper`, no `logrus`, no `zap`, no `aws-sdk-go`, no `minio-go`, no `robfig/cron`, no `gin`, no `echo`, no `grpc-gateway`. CLI built on `flag` + a thin `cmd/` dispatcher. Logger is `log/slog`. HTTP is `net/http`. gRPC is `google.golang.org/grpc` (unavoidable, but its transitive tree is accepted).

### 4.8 Licensing
**Apache 2.0**. Commercial-friendly, patent grant, no SSPL shenanigans. Attribution in `NOTICE`.

---

## 5. Data Model

### 5.1 Persistent Entities (server)

| Entity | Key | Fields |
|--------|-----|--------|
| `Project` | `name` | description, created_at, settings |
| `User` | `id` | email, password_hash, totp_secret, disabled, created_at |
| `Role` | `user+project` | role enum: admin/operator/viewer |
| `Target` | `project+name` | driver, connection_ref (secret id), options, tags, tier, enabled |
| `Storage` | `project+name` | backend, options, credentials_ref, encryption_key_ref |
| `Schedule` | `project+name` | cron, target, type, retention_ref, hooks_ref, enabled |
| `Retention` | `project+name` | rules (json) |
| `Job` | `id` (UUIDv7) | schedule_id, target, type, state, started, finished, size, error, correlation_id |
| `Backup` | `id` (UUIDv7) | job_id, target, type, parent_id, manifest_digest, storage, created_at, tags, protected |
| `Token` | `id` | user_id, scope, expires_at, last_used_at |
| `AuditEvent` | `seq` | ts, actor, action, subject, details_json, prev_hash, this_hash |

Storage: single embedded pure-Go B+Tree file (`kronos.db`, fsync after every transaction, WAL inside). No SQL, no ORM.

### 5.2 Repository (on storage backend)

```
<repo-root>/
  repo.json                       # repo version, encryption header, created_at
  config/
    keys.age                      # encrypted key slots
  data/
    <aa>/<bb>/<blake3-hex>        # content-addressed chunks
  manifests/
    <yyyy>/<mm>/<dd>/<backup-id>.manifest
  streams/                        # PITR WAL/binlog/oplog segments
    <target>/<yyyy-mm-dd>/<segment>
  staging/
    <upload-uuid>/                # in-flight uploads, GC after 24h
  locks/
    <name>.lock                   # advisory locks for cluster-wide ops (GC, key rotate)
  audit/
    <yyyy-mm>.log.gz              # rolled audit archives (optional mirror of server audit)
```

### 5.3 Manifest Format (one backup)

```json
{
  "kronos_manifest": 1,
  "backup_id": "01947f0b-7e90-7a6f-b2ff-3d8d3bb9d4a1",
  "target": "prod-postgres",
  "driver": {"name": "postgres", "version": "17.2", "source_tz": "UTC"},
  "type": "full",
  "parent_id": null,
  "started_at": "2026-04-23T02:00:00Z",
  "finished_at": "2026-04-23T02:07:41Z",
  "compression": "zstd:9",
  "encryption": {"algo": "aes-256-gcm", "key_id": "k7", "kdf": "argon2id"},
  "stats": {
    "logical_size_bytes": 98723456789,
    "stored_size_bytes": 12873456701,
    "chunk_count": 47120,
    "dedup_ratio": 0.87
  },
  "objects": [
    {"schema": "public", "name": "users", "chunks": ["3fa8...", "b21e...", ...]},
    {"schema": "public", "name": "orders", "chunks": [...]}
  ],
  "streams": {
    "wal_start": "0/16B4D98",
    "wal_end":   "0/17C2100"
  },
  "tags": ["prod", "nightly"],
  "signature": "ed25519:..."
}
```

Manifests are themselves stored compressed + encrypted as chunks in `data/`, with a plaintext pointer file in `manifests/` giving the root digest. This means exfiltrating `manifests/` without the key reveals nothing about schema.

---

## 6. Configuration Reference (Summary)

Full YAML reference lives in `docs/CONFIGURATION.md`. Summary:

```yaml
server:
  listen: "0.0.0.0:8500"
  listen_webui: "0.0.0.0:8500"          # same port by default
  data_dir: "/var/lib/kronos"
  tls:
    cert: "/etc/kronos/server.crt"
    key:  "/etc/kronos/server.key"
    client_ca: "/etc/kronos/ca.crt"     # for agent mTLS
  auth:
    oidc:
      issuer: "https://id.example.com"
      client_id: "kronos"
      client_secret: "${env:KRONOS_OIDC_SECRET}"
  master_passphrase: "${file:/run/secrets/kronos-master}"

projects:
  - name: default
    storages:
      - name: primary-s3
        backend: s3
        bucket: "acme-kronos-backups"
        region: "eu-north-1"
        endpoint: "https://s3.eu-north-1.amazonaws.com"
        credentials: "${vault:secret/kronos/s3#creds}"
        encryption_key: "${file:/run/secrets/kronos-age-key}"
    targets:
      - name: prod-postgres
        driver: postgres
        connection:
          host: "10.0.1.5"
          port: 5432
          user: "kronos"
          password: "${vault:secret/kronos/pg#password}"
          database: "*"              # backup all
          tls: require
        agent: "agent-prod-db-1"
        tier: tier0
    schedules:
      - name: prod-pg-nightly
        target: prod-postgres
        type: full
        cron: "0 2 * * *"
        storage: primary-s3
        retention: gfs-standard
        hooks:
          pre_backup:
            - shell: "/usr/local/bin/notify-ops start"
          on_failure:
            - webhook: "https://hooks.example.com/kronos"
      - name: prod-pg-hourly-incr
        target: prod-postgres
        type: incr
        cron: "0 * * * *"
        storage: primary-s3
        retention: last-48
    retentions:
      - name: gfs-standard
        rules:
          gfs: {daily: 7, weekly: 4, monthly: 12, yearly: 3}
      - name: last-48
        rules:
          count: 48
    notifications:
      - name: slack-sre
        type: slack
        webhook: "${env:SLACK_WEBHOOK_SRE}"
      - routing:
          - when: "job.failed"
            channel: slack-sre

agents:
  - name: agent-prod-db-1
    endpoint: "10.0.1.5:8600"
    tls:
      cert: "/etc/kronos/agent.crt"
      key: "/etc/kronos/agent.key"
      server_ca: "/etc/kronos/ca.crt"
```

---

## 7. Acceptance Criteria (MVP)

MVP is the **v0.1.0** release. Acceptance requires every item below to be green.

**Functional**
- ✅ Backup (full + incremental) and restore work end-to-end for all 4 Core databases against a local target and an S3 target.
- ✅ PITR works for PostgreSQL (WAL) and MySQL (binlog) at least.
- ✅ Cron scheduling, GFS retention, mark-and-sweep GC, manifest-level verification all operational.
- ✅ WebUI covers: dashboard, targets, backups, schedules, restore wizard, users. No "TODO" screens shipped.
- ✅ CLI covers every command listed in §3.16.
- ✅ MCP server exposes the listed tools and passes the MCP conformance test.
- ✅ gRPC server↔agent link works across mTLS and survives agent restart mid-backup.

**Non-functional**
- ✅ Binary size ≤ 45 MB stripped, linux/amd64.
- ✅ `go test ./...` passes with race detector on in CI.
- ✅ `govulncheck` clean.
- ✅ Coverage ≥ 80 % (core/drivers/storage packages); ≥ 60 % project-wide.
- ✅ No goroutine leak under 24h soak test at 10 req/s.
- ✅ Benchmarks in `bench/` run in CI and regression alerts are configured.
- ✅ `kronos --help` output, `--version` output, OpenAPI, and proto definitions are auto-published to docs site.
- ✅ All performance targets in §4.1 met on reference hardware.

**Documentation**
- ✅ Quick start (5-minute path from `brew install kronos` to first successful backup).
- ✅ Operations runbook: upgrade, key rotation, agent add/remove, storage migration, disaster recovery.
- ✅ Migration guides: from `pgBackRest`, from `Barman`, from shell scripts.

---

## 8. Out of Scope for v1

Documented for clarity, so we resist scope creep:

- Live replication / standby management.
- Cluster-aware backup of shared-nothing distributed databases (Citus, Vitess) beyond treating them as a collection of independent shards.
- File-system backup.
- UI for writing hook scripts (editor lives outside Kronos).
- Mobile app.
- Kubernetes operator beyond a reference implementation (Phase 2).
- Full-text search inside backups.

---

## 9. Glossary

- **Target** — a database Kronos backs up or restores into.
- **Storage** — a configured repository backend (one bucket / one path).
- **Schedule** — a cron-driven rule that produces Jobs.
- **Job** — one execution of a schedule (or ad-hoc). Produces zero or one Backup.
- **Backup** — a successfully-committed artefact set: one manifest plus the chunks it references, optionally with a stream segment range.
- **Manifest** — the metadata object describing one Backup.
- **Chunk** — content-addressed, compressed, encrypted blob in storage.
- **Repository** — the logical container on a Storage backend that holds all manifests, chunks, streams, and config for one project.
- **Stream** — continuous change data (PG WAL, MySQL binlog, Mongo oplog, Redis AOF) used for PITR.
- **Retention policy** — rules that decide which Backups survive and which get deleted.
- **Agent** — a Kronos process co-located with a database that executes jobs.
- **Server** — the control-plane Kronos process.

---

*End of SPECIFICATION.md*

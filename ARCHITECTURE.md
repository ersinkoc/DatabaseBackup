# Kronos Architecture

Kronos is a zero-dependency Go backup platform for scheduled, encrypted, verified database backups. The repository is currently beyond a skeleton: it contains the core CLI, a control-plane HTTP server, embedded state storage, scheduler orchestration, agent workers, backup and restore pipelines, manifest verification, retention planning, audit logging, webhook notifications, local and S3-compatible storage backends, Redis driver support, a PostgreSQL logical driver MVP, OpenAPI coverage, an operations overview API, and an embedded React/Tailwind WebUI shell.

The project is still in active implementation. The architectural foundation is in place and heavily tested, while broader database driver coverage and some product surfaces described in `.project/IMPLEMENTATION.md` remain roadmap work.

## Quick Navigation

- [Current State](#current-state)
- [High-Level System](#high-level-system)
- [Repository Layout](#repository-layout)
- [Core Package Responsibilities](#core-package-responsibilities)
- [Control-Plane Request Path](#control-plane-request-path)
- [Job Lifecycle](#job-lifecycle)
- [Scheduler And Dispatch](#scheduler-and-dispatch)
- [Backup Data Path](#backup-data-path)
- [Restore Data Path](#restore-data-path)
- [Metadata Model](#metadata-model)
- [Storage Layout](#storage-layout)
- [Security And Integrity](#security-and-integrity)
- [Implementation Status By Area](#implementation-status-by-area)
- [Source Map](#source-map)
- [Roadmap Gaps](#roadmap-gaps)

## Current State

```mermaid
flowchart LR
    done[Implemented foundation]
    active[Active product surface]
    next[Roadmap expansion]

    done --> core[Core domain model]
    done --> kv[Embedded KV store]
    done --> chunk[Chunk, compression, crypto pipeline]
    done --> manifest[Signed manifests]
    done --> storage[Local and S3 storage]

    active --> cli[Single kronos CLI]
    active --> server[HTTP control plane]
    active --> agent[Worker agent]
    active --> scheduler[Persistent scheduler]
    active --> api[REST API and OpenAPI]
    active --> overview[Operations overview API]
    active --> webui[Embedded WebUI dashboard]
    active --> redis[Redis backup driver]
    active --> notify[Webhook notifications with HMAC and retries]

    next --> drivers[Production-grade Postgres, MySQL, MongoDB drivers]
    next --> storageMore[SFTP, Azure, GCS backends]
    next --> hooks[Additional notification channels and hooks]
    next --> advanced[Advanced auth and operations polish]
```

## High-Level System

Kronos is organized as one static Go binary with multiple operating modes:

- `kronos server` runs the control plane.
- `kronos agent --work` runs a worker that claims and executes jobs.
- `kronos local --work` can run a local embedded control plane and worker.
- Administrative commands use the same binary and can operate locally or against the server API.

```mermaid
flowchart TB
    subgraph Operators
        CLI[kronos CLI]
        WebUI[Embedded WebUI]
    end

    subgraph ControlPlane[Control Plane]
        HTTP[net/http API]
        Auth[Token scope checks]
        Scheduler[Scheduler loop]
        Orchestrator[Job orchestrator]
        Stores[Resource stores]
        Audit[Hash-chained audit log]
        Notify[Notification dispatcher]
        Overview[Operations overview]
        Metrics[Prometheus metrics]
    end

    subgraph State[Local State]
        KV[(kvstore state.db)]
        WAL[Rollback WAL]
        BTree[B+Tree buckets]
    end

    subgraph Workers[Agents]
        Agent[Agent worker loop]
        Executor[Backup/restore executor]
        Driver[Database driver]
        Pipeline[Chunk pipeline]
    end

    subgraph Repositories[Backup Repositories]
        Local[(Local filesystem)]
        S3[(S3-compatible object storage)]
    end

    DB[(Database target)]

    CLI --> HTTP
    WebUI --> HTTP
    HTTP --> Auth
    HTTP --> Stores
    HTTP --> Audit
    HTTP --> Notify
    HTTP --> Overview
    HTTP --> Metrics
    Scheduler --> Orchestrator
    Orchestrator --> Stores
    Stores --> KV
    KV --> WAL
    KV --> BTree

    Agent --> HTTP
    Agent --> Executor
    Executor --> Driver
    Driver --> DB
    Executor --> Pipeline
    Pipeline --> Local
    Pipeline --> S3
```

## Repository Layout

```mermaid
flowchart TD
    root[Repository root]
    root --> cmd[cmd/kronos]
    root --> internal[internal]
    root --> api[api/openapi]
    root --> docs[docs]
    root --> project[.project]

    cmd --> cli[CLI command dispatcher and commands]
    cmd --> serverCmd[server, local, agent entrypoints]

    internal --> core[core domain types]
    internal --> config[config and secret expansion]
    internal --> server[control-plane stores and orchestration]
    internal --> agent[agent client and worker loop]
    internal --> drivers[database drivers]
    internal --> engine[driver to pipeline bridge]
    internal --> chunk[chunking, hashing, dedup pipeline]
    internal --> compress[compression adapters]
    internal --> crypto[AEAD, KDF, key slots]
    internal --> repository[manifest commit and load]
    internal --> manifest[signed manifest format]
    internal --> storage[storage backend contract]
    internal --> kvstore[embedded page store]
    internal --> audit[audit chain]
    internal --> retention[retention resolver]
    internal --> schedule[cron and queue logic]
    internal --> restore[restore planning]
    internal --> verify[verification routines]
    internal --> webui[embedded static handler and built assets]

    api --> openapi[Checked OpenAPI spec]
```

## Core Package Responsibilities

```mermaid
classDiagram
    class core {
      Target
      Storage
      Schedule
      Job
      Backup
      Manifest
      RetentionPolicy
      User
      Token
      AuditEvent
    }

    class drivers {
      Driver interface
      BackupFull()
      BackupIncremental()
      Restore()
      Stream()
      ReplayStream()
    }

    class engine {
      BackupFull()
      BackupIncremental()
      Restore()
    }

    class chunk {
      FastCDC
      Pipeline
      Index
      ChunkRef
      Stats
    }

    class storage {
      Backend interface
      Put()
      Get()
      List()
      Delete()
    }

    class repository {
      CommitManifest()
      LoadManifest()
    }

    class server {
      JobStore
      TargetStore
      StorageStore
      ScheduleStore
      BackupStore
      TokenStore
      UserStore
      AgentRegistry
      SchedulerRunner
    }

    class agent {
      Client
      Worker
      Executor
    }

    drivers --> engine
    engine --> chunk
    chunk --> storage
    repository --> storage
    repository --> manifest
    server --> core
    agent --> server
    agent --> core
```

## Control-Plane Request Path

The control plane is built directly on `net/http`. Persistent mode opens `state.db`, creates typed stores, seeds resources from config, starts the scheduler loop, and serves `/healthz`, `/readyz`, `/metrics`, `/api/v1/*`, `/api/v1/overview`, and the embedded WebUI. Health, readiness, metrics, and overview endpoints accept both `GET` and `HEAD` so probes can verify availability without transferring response bodies.

```mermaid
sequenceDiagram
    participant User as CLI or WebUI
    participant HTTP as Control-plane HTTP mux
    participant Auth as Token verifier
    participant Store as Typed store
    participant KV as kvstore
    participant Notify as Notification dispatcher
    participant Audit as Audit log

    User->>HTTP: REST request
    HTTP->>Auth: requireScope(resource:read/write)
    Auth-->>HTTP: allowed
    HTTP->>Store: validate and mutate/query
    Store->>KV: bucket read/write
    KV-->>Store: result
    Store-->>HTTP: domain object
    HTTP->>Notify: terminal job webhook fanout
    HTTP->>Audit: append mutation event
    Audit->>KV: hash-chained append
    HTTP-->>User: JSON response
```

```mermaid
flowchart LR
    UI[WebUI or CLI] --> Overview[/api/v1/overview]
    Overview --> Agents[Agent registry]
    Overview --> Jobs[Job store]
    Overview --> Backups[Backup store]
    Overview --> Inventory[Targets, storage, schedules, policies, notifications, users]
    Overview --> Summary[Compact JSON dashboard summary]
```

## Job Lifecycle

Jobs are persisted in the embedded store so scheduled and manual work survives restarts. On startup, active jobs are failed as `server_lost`; during scheduler ticks, jobs assigned to stale agents can be failed as `agent_lost`.

```mermaid
stateDiagram-v2
    [*] --> queued: schedule tick or manual enqueue
    queued --> running: agent claim
    running --> finalizing: data uploaded, metadata commit starts
    finalizing --> succeeded: backup/restore committed
    running --> succeeded: terminal report
    running --> failed: executor error
    running --> canceled: user cancel
    queued --> canceled: user cancel
    failed --> queued: retry
    canceled --> queued: retry
    succeeded --> [*]
    failed --> [*]
    canceled --> [*]
```

## Scheduler And Dispatch

Schedules use cron expressions, `@between` windows, stable jitter, catch-up policies, and per-target queueing. The server-side scheduler loop turns due schedules into durable queued jobs, and workers claim jobs through the control-plane API.

```mermaid
flowchart LR
    Schedules[ScheduleStore]
    States[ScheduleStateStore]
    Backups[BackupStore]
    Runner[SchedulerRunner]
    Orchestrator[Orchestrator]
    Jobs[(JobStore)]
    Agents[AgentRegistry]
    Worker[Agent worker]

    Schedules --> Runner
    States --> Runner
    Backups --> Runner
    Runner -->|due jobs| Orchestrator
    Orchestrator --> Jobs
    Agents -->|capacity and liveness| Jobs
    Worker -->|claim| Jobs
    Worker -->|finish| Jobs
```

## Backup Data Path

The backup path is streaming-first. A driver emits logical records, the engine serializes them as JSON lines, and the chunk pipeline performs content-defined chunking, BLAKE3 hashing, deduplication, compression, encryption, upload, and manifest reference generation.

```mermaid
flowchart TB
    DB[(Database)]
    Driver[Driver.BackupFull or BackupIncremental]
    Records[Logical records as JSON lines]
    FastCDC[FastCDC content-defined chunks]
    Hash[BLAKE3 hash]
    Dedup{Already indexed?}
    Compress[Compress gzip/zstd/auto]
    Encrypt[AEAD envelope]
    Upload[Upload object]
    Ref[ChunkRef]
    Manifest[Signed manifest]
    Repo[(Storage repository)]
    State[(Backup metadata store)]

    DB --> Driver
    Driver --> Records
    Records --> FastCDC
    FastCDC --> Hash
    Hash --> Dedup
    Dedup -->|yes| Ref
    Dedup -->|no| Compress
    Compress --> Encrypt
    Encrypt --> Upload
    Upload --> Ref
    Ref --> Manifest
    Manifest --> Repo
    Manifest --> State
```

## Chunk Pipeline Worker Graph

The pipeline uses bounded channels and worker groups. References are sorted by sequence before the final manifest is committed, preserving the original stream order even when hash, encode, and upload work happens concurrently.

```mermaid
flowchart LR
    Input[io.Reader]
    Chunker[chunk goroutine]
    HashWorkers[hash workers]
    EncodeWorkers[encode workers]
    UploadWorkers[upload workers]
    Refs[ordered ChunkRefs]

    Input --> Chunker
    Chunker -->|PipelineChunk| HashWorkers
    HashWorkers -->|HashedChunk| EncodeWorkers
    EncodeWorkers -->|EncodedChunk| UploadWorkers
    UploadWorkers -->|ChunkRef| Refs
```

## Restore Data Path

Restore planning validates the backup parent chain before work is enqueued. The agent reconstructs chunk references from storage, decrypts and decompresses payloads, verifies content hashes, and streams records back into the target driver.

```mermaid
sequenceDiagram
    participant User as Operator
    participant API as Control plane
    participant Planner as Restore planner
    participant Worker as Agent worker
    participant Repo as Storage backend
    participant Pipeline as Chunk pipeline
    participant Driver as Database driver
    participant DB as Target database

    User->>API: restore preview/start
    API->>Planner: validate backup chain
    Planner-->>API: restore plan
    API-->>Worker: queued restore job
    Worker->>Repo: load manifests and chunks
    Repo-->>Pipeline: encrypted/compressed payloads
    Pipeline->>Pipeline: decrypt, decompress, hash verify
    Pipeline-->>Driver: logical records
    Driver->>DB: restore records
    Worker->>API: finish succeeded/failed
```

## Metadata Model

The persisted control-plane model is intentionally small and operational. Immutable backup payloads live in object storage, while mutable operational metadata lives in `state.db`.

```mermaid
erDiagram
    TARGET ||--o{ SCHEDULE : has
    STORAGE ||--o{ SCHEDULE : receives
    SCHEDULE ||--o{ JOB : enqueues
    TARGET ||--o{ JOB : runs_against
    STORAGE ||--o{ JOB : writes_to
    JOB ||--o| BACKUP : produces
    BACKUP ||--o{ BACKUP : parent_chain
    RETENTION_POLICY ||--o{ SCHEDULE : applies_to
    USER ||--o{ TOKEN : owns
    USER ||--o{ AUDIT_EVENT : acts

    TARGET {
      string id
      string driver
      string endpoint
    }
    STORAGE {
      string id
      string kind
      string uri
    }
    SCHEDULE {
      string id
      string expression
      bool paused
    }
    JOB {
      string id
      string operation
      string status
      string agent_id
    }
    BACKUP {
      string id
      string manifest_id
      string parent_id
      bool protected
    }
    RETENTION_POLICY {
      string id
      string rules
    }
    USER {
      string id
      string role
    }
    TOKEN {
      string id
      string scopes
    }
    AUDIT_EVENT {
      int seq
      string prev_hash
      string hash
    }
```

## Storage Layout

Kronos separates operational state from repository objects:

```mermaid
flowchart TB
    subgraph StateDB[state.db]
        KVJobs[jobs]
        KVTargets[targets]
        KVStorages[storages]
        KVSchedules[schedules]
        KVBackups[backups]
        KVUsers[users]
        KVTokens[tokens]
        KVAudit[audit events]
    end

    subgraph Repo[Object repository]
        Chunks[chunks by content hash/key]
        Manifests[manifests/YYYY/MM/DD/backup.manifest]
    end

    KVBackups -->|manifest_id/key| Manifests
    Manifests -->|chunk references| Chunks
```

## Security And Integrity

```mermaid
flowchart TD
    Token[Bearer token]
    Scope[Exact scope, resource:*, admin:*, or *]
    Role[User role caps issued scopes]
    API[API mutation]
    Audit[Hash-chained audit event]

    Signing[Ed25519 manifest signing]
    Manifest[Manifest bytes]
    Verify[Signature verification]

    ChunkKey[32-byte chunk key]
    AEAD[AEAD encryption envelope]
    Hash[BLAKE3 chunk hash]
    Chunk[Stored chunk]

    Token --> Scope --> API --> Audit
    Role --> Scope
    Signing --> Manifest --> Verify
    ChunkKey --> AEAD --> Chunk
    Chunk --> Hash
    Hash --> Verify
```

Key points:

- API tokens are stored as hashed verifiers; bearer secrets are shown once.
- Token scopes are checked on protected endpoints.
- API, health, readiness, and metrics responses are marked `Cache-Control:
  no-store`; WebUI HTML revalidates while fingerprinted assets are immutable;
  API and WebUI responses carry baseline browser security headers.
- Manifest signing keys and chunk encryption keys are separate.
- Chunk-level verification decrypts, decompresses, and hashes payloads.
- Administrative mutations can be recorded in a hash-chained audit log.

## Embedded KV Store

The control plane uses a pure-Go embedded key-value store rather than an external database. It is page-based, bucketed, protected by rollback WAL, and has repair coverage.

```mermaid
flowchart LR
    StoreAPI[Typed stores]
    Buckets[Logical buckets]
    BTree[B+Tree]
    Pager[Pager]
    WAL[Rollback WAL]
    File[(state.db)]

    StoreAPI --> Buckets
    Buckets --> BTree
    BTree --> Pager
    Pager --> WAL
    Pager --> File
```

## Build And Verification Pipeline

The repository is Go-first, with the WebUI built separately and embedded into the Go binary.

```mermaid
flowchart LR
    Go[Go packages]
    Tests[go test ./...]
    Vet[go vet ./...]
    Static[staticcheck if installed]
    Vuln[govulncheck if installed]
    UI[web/build.sh]
    Binary[CGO_ENABLED=0 kronos binary]

    Go --> Tests
    Go --> Vet
    Go --> Static
    Go --> Vuln
    UI --> Binary
    Tests --> Binary
    Vet --> Binary
```

Common commands:

```bash
make build
make test
make check
make ui
```

## Implementation Status By Area

| Area | Status | Notes |
| --- | --- | --- |
| CLI | Implemented and broad | Dispatcher plus backup, restore, schedule, storage, target, jobs, audit, token, user, notification, key, health, metrics, overview, GC, and completion commands. |
| Control plane | Implemented foundation | HTTP server, API handlers, token scope checks, stores, scheduler loop, overview JSON, metrics, notifications, and WebUI serving. |
| Agent | Implemented foundation | Heartbeat, resource sync, job claim, execution, finish reporting. |
| State | Implemented | Embedded kvstore with pager, B+Tree, WAL, buckets, tests, and repair path. |
| Backup engine | Implemented foundation | Driver-to-pipeline bridge with full, incremental fallback, and restore streaming. |
| Chunk pipeline | Implemented | FastCDC, BLAKE3, dedup index, compression, encryption envelopes, bounded worker graph. |
| Manifests | Implemented | Signed manifests, commit/load helpers, verification. |
| Storage | Partially implemented | Local filesystem and S3-compatible backend exist; SFTP/Azure/GCS domain kinds fail fast with explicit unsupported-backend errors. |
| Drivers | Partially implemented | Redis/Valkey driver exists; PostgreSQL has a `pg_dump`/`psql` logical MVP with optional `pg_dumpall --globals-only` role metadata capture, multi-version conformance, 15-to-17 restore rehearsal, and full global restore rehearsal coverage; MySQL/MariaDB has a `mysqldump`/`mysql` logical MVP with real-service MySQL 8.4, MariaDB 11.4, bidirectional restore rehearsal conformance, and 10,000-row MySQL/MariaDB restore drills; memory test driver exists; MongoDB remains planned in the blueprint. |
| Retention | Implemented foundation | Count, time, size, and GFS planning plus server-side policy endpoints. |
| Notifications | Implemented foundation | Webhook rules for terminal job events, optional HMAC signatures, bounded retries, API/CLI management, and audit metadata. |
| WebUI | Early product surface | Embedded React/Tailwind operations dashboard build is served by the control plane; live dashboard API support now exists through `/api/v1/overview`. |
| OpenAPI | Implemented | Checked spec under `api/openapi`. |

## Source Map

Use this section as a fast path from architecture concepts to code.

| Concept | Primary files |
| --- | --- |
| CLI dispatcher and command registry | `cmd/kronos/main.go` |
| Control-plane server startup | `cmd/kronos/server.go` |
| Local embedded server/worker mode | `cmd/kronos/local.go` |
| Agent CLI mode | `cmd/kronos/agent.go` |
| Domain model | `internal/core/types.go` |
| HTTP client used by agents and CLI surfaces | `internal/agent/client.go` |
| Agent worker polling loop | `internal/agent/worker.go` |
| Agent job execution | `internal/agent/executor.go` |
| Server orchestration | `internal/server/orchestrator.go` |
| Job persistence | `internal/server/job_store.go` |
| Resource persistence | `internal/server/resource_store.go` |
| Scheduler runner | `internal/server/scheduler_runner.go` |
| Cron/window schedule parsing | `internal/schedule/cron.go`, `internal/schedule/window.go` |
| Driver contract | `internal/drivers/driver.go` |
| Redis driver | `internal/drivers/redis/driver.go` |
| Backup/restore engine bridge | `internal/engine/backup.go` |
| Chunk pipeline | `internal/chunk/pipeline.go` |
| FastCDC chunker | `internal/chunk/fastcdc.go` |
| Chunk index and dedup | `internal/chunk/index.go` |
| Compression adapters | `internal/compress/*.go` |
| Encryption and key material | `internal/crypto/*.go` |
| Manifest format | `internal/manifest/manifest.go` |
| Manifest commit/load | `internal/repository/commit.go` |
| Repository garbage collection | `internal/repository/gc.go` |
| Backup verification | `internal/verify/manifest.go` |
| Storage backend contract | `internal/storage/backend.go` |
| Local storage backend | `internal/storage/local/local.go` |
| S3-compatible backend | `internal/storage/s3/backend.go` |
| Embedded state database | `internal/kvstore/*.go` |
| Audit log | `internal/audit/log.go` |
| Retention planner | `internal/retention/retention.go` |
| Restore planner | `internal/restore/plan.go` |
| Notification rules and dispatcher | `internal/server/notification_store.go` |
| WebUI handler | `internal/webui/handler.go` |
| WebUI assets | `internal/webui/static` |
| OpenAPI contract | `api/openapi/openapi.yaml` |

## Roadmap Gaps

The current codebase has a solid backbone, but several areas are intentionally incomplete or still early:

```mermaid
flowchart TD
    Backbone[Stable backbone]
    Gaps[Remaining gaps]

    Backbone --> CLI[CLI and API surface]
    Backbone --> State[Persistent state]
    Backbone --> Jobs[Scheduler and jobs]
    Backbone --> Pipeline[Backup pipeline]
    Backbone --> Verify[Verification]

    Gaps --> MoreDrivers[Production-grade Postgres, MySQL/MariaDB, and MongoDB drivers]
    Gaps --> MoreStorage[SFTP, Azure Blob, GCS, WebDAV backends]
    Gaps --> Auth[Advanced auth flows and hardening]
    Gaps --> Notifications[Notifications, hooks, and routing]
    Gaps --> WebDepth[Full WebUI workflows beyond dashboard shell]
    Gaps --> E2E[End-to-end integration environments]
```

Recommended next engineering slices:

1. Extend PostgreSQL hardening around operator-scale restore drills and broader upgrade rehearsal evidence.
2. Expand database driver coverage to MongoDB.
3. Deepen the WebUI from operational dashboard shell into resource CRUD and job detail workflows.
4. Add storage backend parity for the domain-level kinds already present in `core.StorageKind`.
5. Harden production auth, token lifecycle, and audit coverage around every mutation.

## Architectural Principles

```mermaid
mindmap
  root((Kronos))
    Single binary
      CLI
      Server
      Agent
      Local mode
    Streaming data path
      Driver records
      Chunk pipeline
      Object storage
    Verifiability
      Signed manifests
      Chunk hashes
      Audit chain
    Operational durability
      Embedded state
      WAL
      Restart recovery
    Minimal runtime dependencies
      Go stdlib server
      Pure-Go storage/state
      Static binary target
```

## What To Read Next

- `README.md` for product status and operator-facing examples.
- `.project/SPECIFICATION.md` for product requirements.
- `.project/IMPLEMENTATION.md` for the full target architecture blueprint.
- `docs/quickstart.md` for first-run usage.
- `docs/operations.md` for production runbooks.
- `api/openapi/openapi.yaml` for the REST contract.

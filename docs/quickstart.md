# Quick Start

This guide starts Kronos locally, creates an admin token, registers a Redis
target and local repository, runs a backup, and previews restore.

## 1. Build

```bash
make build
./bin/kronos version
```

Build metadata is stamped from Git by default. Override `VERSION`, `COMMIT`, or
`BUILD_DATE` if you need fixed release metadata.
Use `make release` when you also want a platform-named artifact and `.sha256`
checksum under `bin/`. Use `make release-all` for linux/darwin amd64/arm64
artifacts.

To embed the React WebUI into the binary, run `make ui` before `make build`.

If `make` is unavailable, build directly:

```bash
go build -trimpath \
  -ldflags "-X github.com/kronos/kronos/internal/buildinfo.Version=dev" \
  -o bin/kronos ./cmd/kronos
```

## 2. Generate Keys

```bash
./bin/kronos keygen --key-id local-dev
export KRONOS_MANIFEST_PRIVATE_KEY=<ed25519-private-key-hex>
export KRONOS_CHUNK_KEY=<32-byte-hex-key>
export KRONOS_BOOTSTRAP_TOKEN=<random-one-time-bootstrap-secret>
```

Keep the printed public key. It is needed for offline manifest verification.

## 3. Start Local Mode

```bash
./bin/kronos local --listen 127.0.0.1:8500 --work \
  --manifest-private-key "$KRONOS_MANIFEST_PRIVATE_KEY" \
  --chunk-key "$KRONOS_CHUNK_KEY" \
  --key-id local-dev
```

In another terminal:

```bash
./bin/kronos health
./bin/kronos ready
```

## 4. Create An Admin Token

Bootstrap the first admin user and copy-once admin token:

```bash
./bin/kronos user bootstrap \
  --id admin \
  --email admin@example.com \
  --display-name Admin \
  --token-name local-cli \
  --bootstrap-token "$KRONOS_BOOTSTRAP_TOKEN"
export KRONOS_TOKEN=<copy-once-secret>
./bin/kronos token verify
```

From this point on, the CLI sends the token automatically when
`KRONOS_TOKEN` is set. The bootstrap endpoint only works while both the user
and token stores are empty; later user and token changes require normal admin
authorization. For non-local deployments, set `server.auth.bootstrap_token` in
the config, preferably through an environment placeholder, and keep the server
bound privately until this step is complete.

## 5. Add Resources

Create a local repository directory:

```bash
mkdir -p /tmp/kronos-repo
./bin/kronos storage add --id local-repo --name local-repo --kind local --uri file:///tmp/kronos-repo
./bin/kronos storage test --uri file:///tmp/kronos-repo
```

This build can execute local and S3-compatible repositories. Other domain-level
storage kinds such as SFTP, Azure Blob, and Google Cloud Storage are still
roadmap backends and fail fast with an explicit unsupported-kind error.

Register a Redis target. Adjust the endpoint if Redis is elsewhere:

```bash
./bin/kronos target add --id redis-local --name redis-local --driver redis --endpoint 127.0.0.1:6379
./bin/kronos target test redis-local --driver redis --endpoint 127.0.0.1:6379
./bin/kronos target inspect --id redis-local
```

Redis/Valkey is the most complete executable database driver in this build.
PostgreSQL also has a logical backup/restore MVP that shells out to `pg_dump`
for full backups and `psql` for restores; install PostgreSQL client tools on
worker agents before using it. The driver strips password material from
process-visible `--dbname` arguments and passes the password through
`PGPASSWORD`. Set target option `include_globals=true` when a
backup should also capture PostgreSQL global role metadata through
`pg_dumpall --globals-only --no-role-passwords`; the agent needs privileges to
read those objects, and role passwords are intentionally excluded. PostgreSQL
restore requires `--replace-existing` and `--yes`; the driver refuses
non-dry-run plain SQL restores without `replace_existing=true` and runs `psql`
in a single transaction. CI exercises PostgreSQL 15, 16, and 17 conformance,
a PostgreSQL 15-to-17 restore rehearsal, and a PostgreSQL 17 full global restore
rehearsal that replays actual globals plus database streams into a separate
target, plus a PostgreSQL 17 10,000-row restore drill. MySQL/MariaDB has a
logical backup/restore MVP that
shells out to `mysqldump` for full backups and `mysql` for restores; install
matching MySQL or MariaDB client tools on worker agents before using it. MySQL
passwords are passed through `MYSQL_PWD` rather than command arguments. MySQL
restore also requires explicit replace-existing intent. CI exercises this path
against real MySQL 8.4 and MariaDB 11.4 services with backup/restore
rehearsals for indexed JSON data, plus bidirectional MySQL/MariaDB restore
rehearsals and 10,000-row MySQL/MariaDB restore drills. MongoDB has a logical
backup/restore MVP that shells out to `mongodump` and `mongorestore` archive
streams; install MongoDB Database Tools on worker agents before using it.
MongoDB passwords are written to a 0600 temporary Database Tools `--config`
file so the process list contains only the config path, not the secret.
MongoDB restores also require explicit replace-existing intent. Set
`oplog=true` in target options only for full replica-set backups; MongoDB
Database Tools reject `--oplog` for database-scoped dumps, so Kronos rejects
targets that combine both. CI exercises the database-scoped archive path
against MongoDB 7.0 and 8.0 with real-service backup/restore conformance, plus
a MongoDB 7.0 10,000-document restore drill.

## 6. Run A Backup

```bash
./bin/kronos backup now --target redis-local --storage local-repo --type full
./bin/kronos jobs list
./bin/kronos backup list
```

If the job fails because Redis is not running, start Redis or point the target
at a reachable Redis/Valkey instance, then retry:

```bash
./bin/kronos jobs retry --id <job-id>
```

## 7. Preview Restore

```bash
./bin/kronos restore preview --backup <backup-id> --target redis-local
```

For an actual restore, confirm explicitly:

```bash
./bin/kronos restore start --backup <backup-id> --target redis-local --replace-existing --yes
```

## 8. Verify And Inspect

```bash
./bin/kronos backup verify \
  --manifest-key <manifest-key> \
  --level chunk \
  --public-key <ed25519-public-key-hex> \
  --chunk-key "$KRONOS_CHUNK_KEY" \
  --storage-local /tmp/kronos-repo

./bin/kronos audit verify
./bin/kronos agent list
./bin/kronos metrics
```

For the full command surface, see [CLI reference](cli.md). For production
procedures, see [Operations runbook](operations.md).

Use `--output json`, `--output pretty`, `--output yaml`, or `--output table`
before a command or alongside command flags to choose machine-readable,
formatted JSON, YAML, or a compact terminal table.

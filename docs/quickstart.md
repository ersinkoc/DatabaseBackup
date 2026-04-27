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

Local/no-token mode accepts unauthenticated requests, so bootstrap the first
user and token from localhost:

```bash
./bin/kronos user add --id admin --email admin@example.com --display-name Admin --role admin
./bin/kronos token create --user admin --name local-cli --scope '*'
export KRONOS_TOKEN=<copy-once-secret>
./bin/kronos token verify
```

From this point on, the CLI sends the token automatically when
`KRONOS_TOKEN` is set.

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
worker agents before using it. Set target option `include_globals=true` when a
backup should also capture PostgreSQL global role metadata through
`pg_dumpall --globals-only --no-role-passwords`; the agent needs privileges to
read those objects, and role passwords are intentionally excluded. PostgreSQL
restore requires `--replace-existing` and `--yes`; the driver refuses
non-dry-run plain SQL restores without `replace_existing=true` and runs `psql`
in a single transaction. MySQL/MariaDB has a logical backup/restore MVP that
shells out to `mysqldump` for full backups and `mysql` for restores; install
matching MySQL or MariaDB client tools on worker agents before using it. MySQL
restore also requires explicit replace-existing intent. CI exercises the MySQL
path against a real MySQL 8.4 service with a backup/restore rehearsal for
indexed JSON data. MongoDB is still a roadmap driver and fails fast with an
explicit unsupported-driver error when probed or executed.

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

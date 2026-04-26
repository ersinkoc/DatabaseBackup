# Operations Runbook

This runbook covers routine Kronos control-plane and agent operations. Commands
assume `./bin/kronos`; add `--server` and `--token` when operating against a
remote control plane.

For topology selection, start with
[Deployment Topologies](deployment-topologies.md). For Kubernetes deployments,
start from [deploy/kubernetes](../deploy/kubernetes/README.md) and replace the
image, configuration, secrets, and network policy with your
environment-specific controls.

## Preflight

```bash
./bin/kronos version
./bin/kronos config validate --config kronos.yaml
./bin/kronos --output pretty config inspect --config kronos.yaml
./bin/kronos token verify
./bin/kronos agent list
./bin/kronos jobs list
```

Before any production change, verify that recent backups exist and that at
least one manifest can be verified:

```bash
./bin/kronos backup list
./bin/kronos backup verify --manifest-key <manifest-key> --level manifest --public-key <public-key-hex> --storage-local <repo-path>
```

For full rehearsal steps, use the [Restore Drills](restore-drills.md) checklist.

For public or shared control planes, set token verification throttling in
`kronos.yaml` to match your expected automation volume:

```yaml
server:
  auth:
    token_verify_rate_limit: 10
    token_verify_rate_window: "1m"
```

Monitor `kronos_auth_rate_limited_total` on `/metrics` for rejected token
verification attempts. A steady increase usually means callers need backoff,
token reuse, or a larger verification budget.

Monitor `kronos_build_info` after deploys to confirm every scraped control-plane
instance is running the intended build metadata.

Monitor `kronos_process_start_timestamp` and `kronos_process_uptime_seconds` to
spot unexpected restarts or uneven rollout age across control-plane instances.

Monitor `kronos_audit_events_total` to confirm the audit chain is growing during
control-plane mutations and scheduled operations.

Monitor `kronos_agents_capacity` to confirm the healthy agent fleet can claim
the expected number of concurrent jobs.

Monitor `kronos_targets_total`, `kronos_storages_total`,
`kronos_schedules_total`, and `kronos_schedules_paused` after config seeding or
resource CRUD changes to catch missing inventory quickly.

Monitor `kronos_targets_by_driver{driver="..."}`,
`kronos_storages_by_kind{kind="..."}`, and
`kronos_schedules_by_type{type="..."}` to catch inventory mix changes after
migrations or rollout waves.

Monitor `kronos_jobs_active` alongside `kronos_agents_capacity` to alert when
running and finalizing work is saturating the fleet.

Monitor `kronos_jobs_by_operation{operation="..."}` and
`kronos_jobs_active_by_operation{operation="..."}` to separate backup pressure
from restore pressure during incidents.

Monitor `kronos_jobs_active_by_agent{agent_id="..."}` to catch uneven work
assignment or a stuck worker before queue depth becomes visible.

Monitor `kronos_backups_protected` before retention changes to make sure
critical restore points are intentionally pinned.

Monitor `kronos_backups_bytes_total` for logical protected-data growth and to
spot unexpectedly large backup runs.

Monitor `kronos_backups_chunks_total` with logical bytes to spot chunking or
deduplication changes after driver or compression updates.

Monitor `kronos_backups{type="..."}` to confirm the expected full,
incremental, differential, stream, or schema backup mix.

Monitor `kronos_backups_by_storage{storage_id="..."}` and
`kronos_backups_bytes_by_storage{storage_id="..."}` to catch uneven storage
distribution before it becomes a capacity problem.

Monitor `kronos_backups_by_target{target_id="..."}` and
`kronos_backups_bytes_by_target{target_id="..."}` to catch fast-growing targets
before they dominate backup windows or retention budgets.

Monitor `kronos_backups_latest_completed_timestamp` and the target/storage
labeled freshness metrics to catch stalled backup coverage before restores are
needed.

Monitor `kronos_retention_policies_total`, `kronos_users_total`,
`kronos_notification_rules_total`, `kronos_notification_rules_enabled`,
`kronos_tokens_total`, `kronos_tokens_revoked`, and `kronos_tokens_expired` to
track administrative inventory, notification coverage, and token cleanup.

Run `./bin/kronos token prune --dry-run` after token rotation windows to preview
revoked or expired token metadata, then `./bin/kronos token prune` to remove it
from the control-plane store.

## Notifications

Kronos can post webhook notifications when jobs reach a terminal state. Configure
top-level notification rules in `kronos.yaml`:

```yaml
notifications:
  - name: ops-failures
    when: job.failed
    webhook: "${env:KRONOS_OPS_WEBHOOK}"
    secret: "${env:KRONOS_OPS_WEBHOOK_SECRET}"
  - name: restore-drills
    events:
      - job.succeeded
      - job.canceled
    webhook: "${env:KRONOS_RESTORE_DRILL_WEBHOOK}"
```

Supported events are `job.succeeded`, `job.failed`, and `job.canceled`. Webhook
delivery is attempted during job finalization and delivery results are recorded
in the `job.finished` audit event metadata. When `secret` is set, Kronos sends
`X-Kronos-Signature: sha256=<hex-hmac>` over the JSON payload. Treat
notification endpoints as production dependencies: use HTTPS, verify signatures,
keep receiver timeouts short, and make the receiver idempotent because retries
may be added in later releases.

Notification rules are also manageable through the control-plane API:

```bash
curl -fsS http://127.0.0.1:8500/api/v1/notifications
curl -fsS -X POST http://127.0.0.1:8500/api/v1/notifications \
  -H 'Content-Type: application/json' \
  -d '{"id":"ops-failures","name":"ops-failures","events":["job.failed"],"webhook_url":"https://hooks.example.com/kronos","enabled":true}'
```

Notification secrets are redacted from API and CLI output by default. Use
`include_secrets=true` only for controlled break-glass inspection paths that
also have `notification:write`.

## Alert Rule Examples

Use these Prometheus rules as a starting point and tune thresholds to match your
backup windows and deployment cadence:

```yaml
groups:
  - name: kronos-control-plane
    rules:
      - alert: KronosControlPlaneRestarted
        expr: kronos_process_uptime_seconds < 300
        for: 5m
        labels:
          severity: warning
        annotations:
          summary: Kronos control plane restarted recently

      - alert: KronosNoBackupFreshness
        expr: time() - kronos_backups_latest_completed_timestamp > 6 * 60 * 60
        for: 15m
        labels:
          severity: critical
        annotations:
          summary: No completed Kronos backup in the expected freshness window

      - alert: KronosAgentCapacitySaturated
        expr: kronos_jobs_active >= kronos_agents_capacity and kronos_agents_capacity > 0
        for: 10m
        labels:
          severity: warning
        annotations:
          summary: Kronos active jobs are saturating healthy agent capacity

      - alert: KronosAuthRateLimited
        expr: increase(kronos_auth_rate_limited_total[15m]) > 0
        for: 5m
        labels:
          severity: warning
        annotations:
          summary: Kronos token verification requests are being rate limited

      - alert: KronosExpiredTokensPresent
        expr: kronos_tokens_expired > 0
        for: 1h
        labels:
          severity: info
        annotations:
          summary: Kronos has expired API tokens that can be cleaned up
```

## Upgrade

1. Build and test the release artifacts:

   ```bash
   make test
   make release-all
   make provenance
   make sbom
   make verify-release
   ./bin/kronos-$(go env GOOS)-$(go env GOARCH) version
   ```

   `make release-all` derives version metadata from Git by default. Set
   `VERSION=<version> COMMIT=<commit> BUILD_DATE=<rfc3339>` when a release
   pipeline needs exact stamped values. Set `RELEASE_TARGETS="linux/amd64"` to
   restrict the target matrix.

2. Publish an immutable release from a signed tag when cutting a production
   version:

   ```bash
   git tag -s v1.2.3 -m "v1.2.3"
   git push origin v1.2.3
   ```

   The `release` workflow runs the full Go test suite, builds the default
   linux/darwin amd64/arm64 binaries through `scripts/release.sh`, verifies all
   checksums, writes `bin/kronos-provenance.json` and
   `bin/kronos-sbom.json`, uploads workflow artifacts, and publishes the tag
   assets to the GitHub release.

3. Drain new work by pausing schedules that should not run during the upgrade:

   ```bash
   ./bin/kronos schedule list
   ./bin/kronos schedule pause --id <schedule-id>
   ./bin/kronos jobs list
   ```

4. Wait for running jobs to finish or cancel jobs that must not continue:

   ```bash
   ./bin/kronos jobs inspect --id <job-id>
   ./bin/kronos jobs cancel --id <job-id>
   ```

5. Stop agents, replace the binary, then restart the control plane and agents.

6. Confirm health and resume schedules:

   ```bash
   curl -fsS http://127.0.0.1:8500/healthz
   curl -fsS http://127.0.0.1:8500/readyz
   ./bin/kronos agent list
   ./bin/kronos schedule resume --id <schedule-id>
   ./bin/kronos schedule tick
   ```

## Key Rotation

Kronos separates manifest signing keys from chunk encryption keys. Rotate them
as separate changes so verification and restore checks stay easy to reason
about.

1. Generate the new key material:

   ```bash
   ./bin/kronos keygen --key-id <new-key-id>
   ```

2. Store the new private signing key and chunk key in the secret manager used by
   the agent environment.

3. Restart agents with the new values:

   ```bash
   export KRONOS_MANIFEST_PRIVATE_KEY=<new-ed25519-private-key-hex>
   export KRONOS_CHUNK_KEY=<new-32-byte-hex-key>
   ./bin/kronos agent --work --key-id <new-key-id>
   ```

4. Keep old public signing keys and old chunk keys until every backup encrypted
   with them has expired and retention has removed it.

For repository root-key slot material, keep an escrow copy before rotation and
write the rotated slot file to a new path first:

```bash
./bin/kronos key escrow export --file keys.json --out keys-escrow.json
./bin/kronos key rotate --file keys.json --out keys-rotated.json --id ops-rotated --unlock-slot ops --unlock-passphrase-env KRONOS_KEY_PASSPHRASE --passphrase-env KRONOS_ROTATED_PASSPHRASE
```

5. Verify both old and new backups before deleting old key material:

   ```bash
   ./bin/kronos backup verify --manifest-key <old-manifest-key> --level chunk --public-key <old-public-key-hex> --chunk-key <old-chunk-key-hex> --storage-local <repo-path>
   ./bin/kronos backup verify --manifest-key <new-manifest-key> --level chunk --public-key <new-public-key-hex> --chunk-key <new-chunk-key-hex> --storage-local <repo-path>
   ```

## Agent Add Or Remove

Add an agent:

```bash
./bin/kronos token create --user <agent-user> --name <agent-name> --scope agent:write,job:write,target:read,storage:read,backup:read
export KRONOS_TOKEN=<copy-once-secret>
./bin/kronos agent --work --id <agent-id> --capacity <n> --server <server-url> --key-id <key-id>
./bin/kronos agent inspect --id <agent-id>
```

Pin a target to a specific agent when needed:

```bash
./bin/kronos target update --id <target-id> --agent <agent-id> --name <name> --driver redis --endpoint <host:port>
```

Remove an agent:

```bash
./bin/kronos agent inspect --id <agent-id>
./bin/kronos jobs list
./bin/kronos target update --id <target-id> --name <name> --driver redis --endpoint <host:port>
```

Stop the agent process after its running jobs finish. If the agent is lost, the
server marks stale running jobs failed with `agent_lost`; retry them once a
healthy agent is available:

```bash
./bin/kronos jobs retry --id <job-id>
```

## Storage Migration

1. Register the new repository:

   ```bash
   ./bin/kronos storage add --id <new-storage-id> --name <name> --kind local --uri file:///backup/kronos
   ./bin/kronos storage test --uri file:///backup/kronos
   ```

2. Create or update schedules to use the new storage:

   ```bash
   ./bin/kronos schedule update --id <schedule-id> --name <name> --target <target-id> --storage <new-storage-id> --type full --cron "<expr>"
   ```

3. Keep the old storage registered until old backups expire or are deliberately
   removed by retention.

4. Verify at least one backup from both repositories before retiring old
   storage.

## Request Correlation

Kronos echoes `X-Kronos-Request-ID` on control-plane responses and records it in
audit metadata for mutations. When investigating an incident, provide a stable
request ID from the CLI and use it when comparing CLI errors, server logs, and
audit records:

```bash
./bin/kronos --request-id incident-20260426-001 --server http://127.0.0.1:8500 backup now --target <target-id> --storage <storage-id>
./bin/kronos audit search --query incident-20260426-001
```

If `--request-id` is omitted, CLI and agent requests generate correlation IDs
automatically. Failed CLI and agent control-plane requests include the response
request ID in the error text when the server provides one.

## Disaster Recovery

1. Start a clean control plane with the preserved state database and config:

   ```bash
   ./bin/kronos repair-db --db <state.db>
   ./bin/kronos server --config kronos.yaml
   ```

2. Recreate users and tokens if the token store was not recovered:

   ```bash
   ./bin/kronos user add --id admin --email admin@example.com --display-name Admin --role admin
   ./bin/kronos token create --user admin --name recovery --scope '*'
   ```

3. Confirm targets, storages, schedules, backups, and agents:

   ```bash
   ./bin/kronos target list
   ./bin/kronos storage list
   ./bin/kronos schedule list
   ./bin/kronos backup list
   ./bin/kronos agent list
   ```

4. Preview before restore, then enqueue restore:

   ```bash
   ./bin/kronos restore preview --backup <backup-id> --target <target-id>
   ./bin/kronos restore start --backup <backup-id> --target <target-id> --replace-existing --yes
   ./bin/kronos jobs inspect --id <restore-job-id>
   ```

5. Verify the audit chain after recovery:

   ```bash
   ./bin/kronos audit verify
   ./bin/kronos audit tail --limit 20
   ```

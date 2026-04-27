# Kronos CLI Reference

Kronos ships as one binary with subcommands. All control-plane commands accept
`--server`; pass global `--server`, `--token`,
`--output json|pretty|yaml|table`, `--request-id`, or `--no-color` before the
command. `--token`, `--output`, `--request-id`, and `--no-color` may also be
placed with command flags, and `KRONOS_TOKEN` sends a bearer token when
`--token` is omitted.
The server keeps local/no-token mode open for development, but enforces token
scopes when a bearer token is provided. Exact scopes, `resource:*`, `admin:*`,
and `*` are accepted. Color is TTY-aware and can also be disabled with
`NO_COLOR=1`. When `--request-id` is supplied, Kronos sends it as
`X-Kronos-Request-ID`; otherwise CLI commands generate one automatically.

## Core

```bash
kronos help
kronos help backup
kronos version
kronos keygen --key-id prod-2026
kronos health
kronos ready
kronos --server http://127.0.0.1:8500 --token "$KRONOS_TOKEN" --output pretty backup list
kronos --request-id incident-20260426-001 backup list --server http://127.0.0.1:8500
kronos config validate --config kronos.yaml
kronos --output pretty config inspect --config kronos.yaml
kronos server --config kronos.yaml
kronos agent --server http://127.0.0.1:8500 --token "$KRONOS_TOKEN" --capacity 2
kronos agent --work --server http://127.0.0.1:8500 --token "$KRONOS_TOKEN" --manifest-private-key <ed25519-private-key-hex> --chunk-key <32-byte-hex-key> --key-id prod-2026
kronos agent list
kronos agent inspect --id agent-1
kronos metrics
kronos overview
kronos --output table overview
kronos local --listen 127.0.0.1:8500
kronos local --config kronos.yaml --work --manifest-private-key <ed25519-private-key-hex> --chunk-key <32-byte-hex-key> --key-id prod-2026
```

`kronos agent` defaults to heartbeat-only mode. Add `--work` to sync resources,
claim jobs, execute backups/restores, and report terminal status. Worker tokens
need `agent:write`, `job:write`, `target:read`, `storage:read`, and
`backup:read`.

## Resources

```bash
kronos target add --name redis --driver redis --endpoint 127.0.0.1:6379 --database 0 --user backup --password "$REDIS_PASSWORD" --tls disable --agent agent-1 --tier tier0
kronos target test redis --driver redis --endpoint 127.0.0.1:6379 --database 0 --user backup --password "$REDIS_PASSWORD"
kronos target list
kronos target inspect --id target-1
kronos target update --id target-1 --name redis --driver redis --endpoint 127.0.0.1:6380 --agent agent-2
kronos target remove --id target-1

kronos storage add --name repo --kind local --uri file:///var/lib/kronos/repo
kronos storage add --name s3 --kind s3 --uri s3://kronos-backups --region eu-north-1 --endpoint https://s3.eu-north-1.amazonaws.com --credentials env
kronos storage add --name minio --kind s3 --uri s3://kronos --region us-east-1 --endpoint http://127.0.0.1:9000 --access-key "$S3_ACCESS_KEY" --secret-key "$S3_SECRET_KEY" --force-path-style
kronos storage test --uri file:///var/lib/kronos/repo
kronos storage test --uri s3://kronos --region us-east-1 --endpoint http://127.0.0.1:9000 --access-key "$S3_ACCESS_KEY" --secret-key "$S3_SECRET_KEY" --force-path-style
kronos storage du --uri file:///var/lib/kronos/repo --prefix data/
kronos storage du --uri s3://kronos --prefix data/ --region us-east-1 --endpoint http://127.0.0.1:9000 --access-key "$S3_ACCESS_KEY" --secret-key "$S3_SECRET_KEY" --force-path-style
kronos storage list
kronos storage inspect --id storage-1
kronos storage update --id storage-1 --name repo --kind local --uri file:///var/lib/kronos/repo2
kronos storage remove --id storage-1
```

## Backups

```bash
kronos backup now --target target-1 --storage storage-1 --type full
kronos backup now --target target-1 --storage storage-1 --parent backup-1
kronos backup list
kronos backup list --target target-1 --storage storage-1 --type full --since 7d --protected false
kronos backup inspect --id backup-1
kronos backup protect --id backup-1
kronos backup unprotect --id backup-1
kronos backup verify --manifest manifest.json --public-key <hex> --storage-local /repo
```

List time filters accept RFC3339 timestamps or durations such as `7d`, `24h`,
and `2h`; when both `--since` and `--until` are provided, `--since` must not be
later than `--until`.

## Scheduling And Jobs

```bash
kronos schedule add --name nightly --target target-1 --storage storage-1 --cron "0 2 * * *"
kronos schedule list
kronos schedule inspect --id schedule-1
kronos schedule update --id schedule-1 --name hourly --target target-1 --storage storage-1 --type incr --cron "0 * * * *" --retention-policy policy-1
kronos schedule pause --id schedule-1
kronos schedule resume --id schedule-1
kronos schedule tick
kronos schedule remove --id schedule-1

kronos jobs list
kronos jobs list --status running --operation backup --target target-1 --agent agent-1 --since 2h
kronos jobs inspect --id job-1
kronos jobs cancel --id job-1
kronos jobs retry --id job-1
```

## Notifications

```bash
kronos notification add --id ops-failures --name ops-failures --event job.failed --webhook-url https://hooks.example.com/kronos --secret "$KRONOS_OPS_WEBHOOK_SECRET" --max-attempts 2
kronos notification add --name restore-drills --event job.succeeded,job.canceled --webhook-url https://hooks.example.com/restore-drills
kronos notification list
kronos notification inspect --id ops-failures
kronos notification update --id ops-failures --name ops-failures --event job.failed --webhook-url https://hooks.example.com/kronos --enabled=false
kronos notification remove --id ops-failures
```

## Retention And Restore

```bash
kronos retention plan --input retention.json
kronos retention apply --server 127.0.0.1:8500 --input retention.json --dry-run
kronos retention policy add --input policy.json
kronos retention policy list
kronos retention policy inspect --id policy-1
kronos retention policy update --id policy-1 --input policy.json
kronos retention policy remove --id policy-1
kronos restore preview --backup backup-1 --target restore-target
kronos restore preview --backup backup-1 --target restore-target --at 2026-04-25T12:00:00Z
kronos restore start --backup backup-1 --target restore-target --dry-run
kronos restore start --backup backup-1 --target restore-target --replace-existing --yes
kronos gc --storage-local /repo --public-key <hex> --dry-run
```

Restore `--at` values must be RFC3339 timestamps and cannot point into the
future.

## Users, Tokens, And Audit

```bash
kronos user add --email ops@example.com --display-name "Ops" --role admin
kronos user list
kronos user inspect --id user-1
kronos user grant --id user-1 --role operator
kronos user remove --id user-1

kronos token create --user user-1 --name ci --scope backup:read,backup:write
kronos token verify
kronos token list
kronos token inspect --id token-1
kronos token revoke --id token-1
kronos token prune --dry-run
kronos token prune

kronos audit list --action backup.requested --resource-type job --since 24h --limit 50
kronos audit tail --resource-type job --limit 20
kronos audit search --query backup --actor admin --since 7d
kronos audit verify
```

Common scope families are `backup`, `target`, `storage`, `schedule`, `job`,
`notification`, `retention`, `restore`, `audit`, `token`, `user`, `agent`, and
`metrics`, each using `:read` or `:write` where applicable. `overview` uses
`metrics:read`. Requested token
scopes are capped by the token user's role.

## Keys

```bash
kronos keygen --key-id prod-2026
kronos key add-slot --file keys.json --id ops --generate-root-key --passphrase-env KRONOS_KEY_PASSPHRASE
kronos key add-slot --file keys.json --id breakglass --unlock-slot ops --unlock-passphrase-env KRONOS_KEY_PASSPHRASE --passphrase-env KRONOS_BREAKGLASS_PASSPHRASE
kronos key list --file keys.json
kronos key remove-slot --file keys.json --id breakglass
kronos key escrow export --file keys.json --out keys-escrow.json
kronos key rotate --file keys.json --out keys-rotated.json --id ops-rotated --unlock-slot ops --unlock-passphrase-env KRONOS_KEY_PASSPHRASE --passphrase-env KRONOS_ROTATED_PASSPHRASE
```

Key slot files wrap a 32-byte repository root key with Argon2id-derived
passphrase slots. Rotation creates a new slot file for a new root key; it does
not rewrite existing manifests or chunks.

## Completion

```bash
kronos completion bash
kronos completion zsh
kronos completion fish
```

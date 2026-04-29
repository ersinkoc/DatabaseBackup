# Project Roadmap

> Based on comprehensive codebase analysis performed on 2026-04-29
> This roadmap prioritizes work needed to bring the project to production quality.

## Current State Assessment

Kronos has a strong core: CLI, HTTP control plane, agent worker, persistent KV state, local/S3 repositories, chunking/compression/encryption, manifests, retention, audit, metrics, release scripts, Kubernetes single-replica examples, and good Go test coverage. The project is **not yet production-ready for the full specification** because native database protocols, PITR, advanced auth, secure secret storage, gRPC/mTLS, MCP, and several storage backends are absent. The most urgent decision is whether to honor the original no-shell-out promise or revise the product specification around external tool drivers.

## Phase 1: Critical Fixes (Week 1-2)

### Must-fix items blocking basic functionality

- [x] Replace `bufio.Scanner` restore record reading in `internal/engine/backup.go` — completed 2026-04-29; restore now uses `json.Decoder` and has a 128 KiB record regression test.
- [x] Gate unauthenticated local/no-token mode behind explicit `--dev-insecure` and refuse non-loopback unauthenticated listeners; completed 2026-04-29. `kronos server` is auth-required by default; `kronos local` keeps loopback-only dev ergonomics and requires `--dev-insecure` for non-loopback unauthenticated use.
- [x] Stop passing PostgreSQL and MongoDB passwords in process-visible command arguments while tool-wrapper drivers remain; completed 2026-04-29. PostgreSQL uses `PGPASSWORD`; MongoDB uses a 0600 temporary `--config` file per MongoDB Database Tools guidance.
- [x] Decide and document driver strategy: native protocol implementation vs formally supported external-tool MVP; completed 2026-04-29 in `docs/decisions/0002-external-tool-driver-mvp.md`. Current release treats PostgreSQL/MySQL/MongoDB as external-tool MVPs and keeps native protocol/PITR as roadmap work.
- [x] Ensure CI/local race tests can run by installing a C compiler or documenting non-race fallback honestly; completed 2026-04-29 in `docs/production-readiness.md`. Race tests require CGO plus `gcc`/`clang`; plain tests are documented as fallback only, not equivalent coverage.

## Phase 2: Core Completion (Week 3-6)

### Complete missing core features from specification

- [ ] Implement native PostgreSQL logical backup/restore subset or explicitly demote native PG to post-MVP; spec §3.1.1; current gap is shell-out `pg_dump`/`psql`; effort 2-4 weeks.
- [ ] Implement PostgreSQL WAL/PITR MVP if v0.1 still requires PITR; spec §3.1.1/§3.2/§3.9; effort 2-3 weeks.
- [ ] Implement native MySQL protocol/binlog or formalize `mysqldump` as limited MVP; spec §3.1.2; effort 2-4 weeks.
- [ ] Implement MongoDB OP_MSG/BSON/oplog or formalize `mongodump` MVP; spec §3.1.3; effort 2-4 weeks.
- [ ] Add Redis RDB/PSYNC/AOF stream path beyond SCAN/DUMP; spec §3.1.4; effort 1-2 weeks.
- [ ] Add gRPC or revise agent protocol spec around hardened HTTP polling; spec §3.16; effort 1-2 weeks.
- [ ] Implement SFTP, Azure Blob, and GCS backends or move them out of Phase 1/v0.1 claims; spec §3.3; effort 2-4 weeks.

## Phase 3: Hardening (Week 7-8)

### Security, error handling, edge cases

- [x] Add first-admin bootstrap for empty user/token stores.
- [ ] Add password hashing and role enforcement beyond token scopes.
- [ ] Add TOTP for admin or remove that claim from MVP docs.
- [ ] Add TLS/mTLS deployment path and secure agent enrollment.
- [ ] Move stored credentials to secret references or encrypted-at-rest secret records.
- [ ] Normalize JSON error response format across all REST handlers.
- [ ] Add broad input validation for resource IDs, URI schemes, cron expressions, and option schemas.
- [ ] Add request/body size limits and timeouts to API handlers.
- [ ] Add CORS policy only if browser origins beyond same-origin are supported.

## Phase 4: Testing (Week 9-10)

### Comprehensive test coverage

- [x] Add large-record backup/restore regression tests for SQL dumps and Mongo archives.
- [ ] Add race-enabled CI with a working C compiler.
- [ ] Add failure-injection tests for missing/corrupt chunks and manifest mismatch.
- [ ] Add API integration tests for every endpoint family with unauthorized/forbidden/error paths.
- [ ] Add frontend Vitest component tests and Playwright smoke tests for dashboard/resource/restore flows.
- [ ] Add accessibility checks with axe for the WebUI.
- [ ] Add more retention edge cases for protected backups and parent chains.
- [ ] Add local S3 compatibility tests against MinIO in CI if not already covered by mock tests.

## Phase 5: Performance & Optimization (Week 11-12)

### Performance tuning and optimization

- [ ] Replace S3 whole-object temp spooling where practical with direct chunk upload/retry semantics.
- [ ] Benchmark chunk pipeline throughput against spec targets and store results in CI artifacts.
- [ ] Benchmark KV store under realistic resource/job/backup counts and compare against bbolt fallback threshold.
- [ ] Add scheduler scale test for 10k schedules and publish timing.
- [ ] Add memory profiling for agent backup and restore paths.
- [ ] Add WebUI bundle-size CI guard using current gzip output baseline of 75.87 kB JS.

## Phase 6: Documentation & DX (Week 13-14)

### Documentation and developer experience

- [ ] Update public README/spec claims to match actual driver behavior.
- [ ] Add `CHANGELOG.md`.
- [ ] Generate rendered API docs from `api/openapi/openapi.yaml`.
- [ ] Split production readiness docs into implemented-path readiness vs full-product readiness.
- [ ] Add troubleshooting guide for token scopes, agent claims, storage credentials, and restore failures.
- [ ] Add architecture decision records for shell-out driver MVP, HTTP polling vs gRPC, and secret storage.

## Phase 7: Release Preparation (Week 15-16)

### Final production preparation

- [ ] Run signed-tag release rehearsal and archive checksum/signature/attestation evidence.
- [ ] Add immutable image digest guidance and example overlays.
- [ ] Add systemd units if bare-metal Linux is a supported production target.
- [ ] Add rollback procedure for state DB and binary upgrades.
- [ ] Add monitoring dashboard examples for Prometheus metrics.
- [ ] Add release artifact vulnerability/SBOM verification to the documented gate.

## Beyond v1.0: Future Enhancements

- [ ] MCP server with approval-gated mutating tools.
- [ ] OIDC providers and project-scoped RBAC.
- [ ] OpenTelemetry tracing.
- [ ] Hook execution subsystem.
- [ ] Slack, Discord, email, PagerDuty, Opsgenie, Teams, Telegram channels.
- [ ] HA control plane or externalized state backend.
- [ ] WebDAV, FTP/FTPS, package manager distribution, Helm chart beyond raw manifests.

## Effort Summary

| Phase | Estimated Hours | Priority | Dependencies |
|---|---:|---|---|
| Phase 1 | 80h | CRITICAL | None |
| Phase 2 | 360h | HIGH | Driver strategy decision |
| Phase 3 | 180h | HIGH | Phase 1 |
| Phase 4 | 160h | HIGH | Phase 1-3 |
| Phase 5 | 120h | MEDIUM | Stable core paths |
| Phase 6 | 80h | MEDIUM | Accurate scope decisions |
| Phase 7 | 80h | MEDIUM | Tests/docs green |
| **Total** | **1,060h** | | |

## Risk Assessment

| Risk | Probability | Impact | Mitigation |
|---|---|---|---|
| Native database protocols take much longer than planned | High | High | Decide whether v1 supports external-tool drivers or narrow supported databases. |
| Custom KV store has latent durability bugs | Medium | High | Run chaos/soak tests; keep bbolt fallback decision alive. |
| Secrets stored in state DB cause compliance failure | High | High | Move to encrypted secret records or reference-only external secret model. |
| Agent/control-plane HTTP lacks production transport guarantees | Medium | High | Add TLS/mTLS and explicit enrollment or implement planned gRPC+mTLS. |
| Docs overstate production readiness | High | Medium | Split implemented-path readiness from full-product claims. |
| Frontend grows unmaintainable as one file | High | Medium | Split components/hooks/API client before adding more workflows. |

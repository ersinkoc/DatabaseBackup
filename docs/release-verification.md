# Release Verification

Use this checklist before promoting a downloaded Kronos release into a
production environment. It verifies the artifact bytes, the keyless cosign
signature, the SBOM module coverage and vulnerability scan, and the
GitHub-hosted build/SBOM attestations.

## Inputs

Before cutting a production tag, verify local signing readiness without pushing
anything:

```bash
./scripts/check-release-signing.sh <tag>
```

This confirms `user.signingkey` is configured, the matching GPG secret key is
available, the tag is not already present locally or on `origin`, and a
temporary signed probe tag can be created and verified.

After pushing the release tag, verify the signed tag object from `origin`:

```bash
./scripts/verify-release-tag.sh <tag>
```

This fetches the tag if needed and runs `git verify-tag` so the release evidence
can distinguish "tag exists" from "tag signature verified".

Download the release assets for the target platform into one directory:

```bash
mkdir -p bin
gh release download <tag> --repo ersinkoc/Kronos --dir bin
```

For each binary, keep the matching `.sha256` and `.bundle` files. Keep
`kronos-provenance.json` and `kronos-sbom.json` with their own `.bundle` files
as well. Older releases may use `.sig` plus `.pem` pairs; the verification
script accepts both formats.

## Checksum Verification

```bash
./scripts/verify-release.sh bin
```

This confirms every `kronos-*` binary has a matching SHA-256 checksum and that
the release metadata files are present.

## SBOM And Vulnerability Verification

Install `govulncheck`, then verify the SBOM covers the current Go module graph
and run the vulnerability scan used by the release gate:

```bash
KRONOS_REQUIRE_GOVULNCHECK=1 ./scripts/verify-sbom.sh bin
```

This validates `kronos-sbom.json` against `go list -m all` and runs
`govulncheck ./...`. It is a source/module vulnerability gate, not a standalone
binary artifact scanner.

## Keyless Signature Verification

Install `cosign`, then verify every binary and metadata payload:

```bash
COSIGN_CERTIFICATE_IDENTITY_REGEXP='https://github.com/ersinkoc/Kronos/.github/workflows/release.yml@refs/tags/v.*' \
  ./scripts/verify-signatures.sh bin
```

The verification requires the GitHub Actions OIDC issuer
`https://token.actions.githubusercontent.com` by default. Override
`COSIGN_CERTIFICATE_OIDC_ISSUER` only when validating artifacts from a different
trusted issuer.

## GitHub Attestation Verification

Use a recent GitHub CLI with `gh attestation verify` support:

```bash
gh attestation verify bin/kronos-linux-amd64 \
  --repo ersinkoc/Kronos \
  --signer-workflow .github/workflows/release.yml
```

Repeat the command for the platform binary being promoted. By default, the
GitHub CLI verifies build provenance attestations. To inspect the SBOM
attestation payload, request the SBOM predicate type and output JSON:

```bash
gh attestation verify bin/kronos-linux-amd64 \
  --repo ersinkoc/Kronos \
  --signer-workflow .github/workflows/release.yml \
  --predicate-type https://spdx.dev/Document \
  --format json
```

## Promotion Record

Record the release tag, binary filename, SHA-256 digest, Git commit,
verification operator, verification time, cosign result, and attestation result.
Keep the record with the same retention policy as deployment approvals.

To archive the local checksum, artifact signature, release tag signature, and
artifact digest verification logs, run:

```bash
KRONOS_RELEASE_TAG=<tag> ./scripts/archive-release-evidence.sh bin release-evidence/<tag>
./scripts/verify-release-evidence.sh release-evidence/<tag>
```

Set `GH_ATTESTATION_REPO=ersinkoc/Kronos` to include GitHub provenance and SBOM
attestation verification output in the same evidence directory. When
`KRONOS_RELEASE_TAG` is set, the archive also captures `git verify-tag` output
from `./scripts/verify-release-tag.sh`. Set `KRONOS_REQUIRE_ATTESTATIONS=1`
when verifying production promotion evidence that must include GitHub
attestation logs.

To rehearse the full consumer-side verification path from GitHub release assets,
run:

```bash
./scripts/release-rehearsal.sh <tag> release-evidence/<tag>
```

Do not promote a release if any checksum, SBOM, vulnerability, signature, or
attestation check fails.

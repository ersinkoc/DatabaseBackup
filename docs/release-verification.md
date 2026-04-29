# Release Verification

Use this checklist before promoting a downloaded Kronos release into a
production environment. It verifies the artifact bytes, the keyless cosign
signature, and the GitHub-hosted build/SBOM attestations.

## Inputs

Download the release assets for the target platform into one directory:

```bash
mkdir -p bin
gh release download <tag> --repo ersinkoc/Kronos --dir bin
```

For each binary, keep the matching `.sha256`, `.sig`, and `.pem` files. Keep
`kronos-provenance.json` and `kronos-sbom.json` with their own `.sig` and `.pem`
files as well.

## Checksum Verification

```bash
./scripts/verify-release.sh bin
```

This confirms every `kronos-*` binary has a matching SHA-256 checksum and that
the release metadata files are present.

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

Do not promote a release if any checksum, signature, or attestation check fails.

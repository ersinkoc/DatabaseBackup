#!/usr/bin/env sh
set -eu

dir="${1:-bin}"
issuer="${COSIGN_CERTIFICATE_OIDC_ISSUER:-https://token.actions.githubusercontent.com}"
identity_regexp="${COSIGN_CERTIFICATE_IDENTITY_REGEXP:-}"

# Verify release binaries plus provenance/SBOM metadata payload signatures.
if [ ! -d "$dir" ]; then
	echo "release directory not found: $dir" >&2
	exit 1
fi

if ! command -v cosign >/dev/null 2>&1; then
	echo "cosign is required to verify release signatures" >&2
	exit 1
fi

if [ -z "$identity_regexp" ]; then
	if [ -n "${GITHUB_REPOSITORY:-}" ]; then
		identity_regexp="https://github.com/${GITHUB_REPOSITORY}/.github/workflows/release.yml@.*"
	else
		identity_regexp="https://github.com/.+/.github/workflows/release.yml@.*"
	fi
fi

found=0
for artifact in "$dir"/kronos-*; do
	[ -f "$artifact" ] || continue
	case "$artifact" in
		*.sha256 | *.sig | *.pem | *.tmp) continue ;;
	esac
	found=1
	signature="$artifact.sig"
	certificate="$artifact.pem"
	if [ ! -s "$signature" ] || [ ! -s "$certificate" ]; then
		echo "missing signature or certificate for $artifact" >&2
		exit 1
	fi
	cosign verify-blob \
		--certificate "$certificate" \
		--signature "$signature" \
		--certificate-identity-regexp "$identity_regexp" \
		--certificate-oidc-issuer "$issuer" \
		"$artifact"
	echo "$artifact: signature OK"
done

if [ "$found" -eq 0 ]; then
	echo "no release payloads found in $dir" >&2
	exit 1
fi

#!/usr/bin/env sh
set -eu

release_dir="${1:-bin}"
evidence_dir="${2:-release-evidence}"
repo="${GH_ATTESTATION_REPO:-}"
workflow="${GH_ATTESTATION_WORKFLOW:-.github/workflows/release.yml}"
release_tag="${KRONOS_RELEASE_TAG:-unknown}"

if [ ! -d "$release_dir" ]; then
	echo "release directory not found: $release_dir" >&2
	exit 1
fi

mkdir -p "$evidence_dir"

run_and_capture() {
	name="$1"
	shift
	log="$evidence_dir/$name.log"
	"$@" >"$log" 2>&1
}

run_and_capture checksum ./scripts/verify-release.sh "$release_dir"
run_and_capture signatures ./scripts/verify-signatures.sh "$release_dir"
if [ "$release_tag" != "unknown" ]; then
	run_and_capture tag-signature ./scripts/verify-release-tag.sh "$release_tag"
else
	printf '%s\n' "KRONOS_RELEASE_TAG not set; release tag signature verification not run." >"$evidence_dir/tag-signature.log"
fi

digests="$evidence_dir/artifact-digests.txt"
: >"$digests"
found=0
for artifact in "$release_dir"/kronos-*; do
	[ -f "$artifact" ] || continue
	case "$artifact" in
		*.sha256 | *.json | *.sig | *.pem | *.bundle | *.tmp) continue ;;
	esac
	found=1
	if command -v sha256sum >/dev/null 2>&1; then
		sha256sum "$artifact" >>"$digests"
	elif command -v shasum >/dev/null 2>&1; then
		shasum -a 256 "$artifact" >>"$digests"
	else
		echo "sha256sum or shasum is required to archive release evidence" >&2
		exit 1
	fi
done

if [ "$found" -eq 0 ]; then
	echo "no release payloads found in $release_dir" >&2
	exit 1
fi

attestation_log="$evidence_dir/attestations.log"
if [ -n "$repo" ]; then
	if ! command -v gh >/dev/null 2>&1; then
		echo "gh is required when GH_ATTESTATION_REPO is set" >&2
		exit 1
	fi
	: >"$attestation_log"
	for artifact in "$release_dir"/kronos-*; do
		[ -f "$artifact" ] || continue
		case "$artifact" in
			*.sha256 | *.json | *.sig | *.pem | *.bundle | *.tmp) continue ;;
		esac
		{
			echo "## $artifact provenance"
			gh attestation verify "$artifact" --repo "$repo" --signer-workflow "$workflow"
			echo
			echo "## $artifact SBOM"
			gh attestation verify "$artifact" --repo "$repo" --signer-workflow "$workflow" --predicate-type https://spdx.dev/Document --format json
			echo
		} >>"$attestation_log" 2>&1
	done
else
	printf '%s\n' "GH_ATTESTATION_REPO not set; attestation verification not run." >"$attestation_log"
fi

summary="$evidence_dir/summary.txt"
{
	echo "release_tag=$release_tag"
	echo "release_dir=$release_dir"
	echo "evidence_dir=$evidence_dir"
	echo "git_commit=$(git rev-parse --short HEAD 2>/dev/null || echo unknown)"
	echo "verified_at=$(date -u +%Y-%m-%dT%H:%M:%SZ)"
	echo "checksum_log=checksum.log"
	echo "signature_log=signatures.log"
	echo "tag_signature_log=tag-signature.log"
	echo "attestation_log=attestations.log"
	echo "digests=artifact-digests.txt"
} >"$summary"

./scripts/verify-release-evidence.sh "$evidence_dir" >/dev/null

echo "release evidence archived in $evidence_dir"

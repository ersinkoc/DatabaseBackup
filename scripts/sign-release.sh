#!/usr/bin/env sh
set -eu

dir="${1:-bin}"

# Sign release binaries plus provenance/SBOM metadata payloads.
if [ ! -d "$dir" ]; then
	echo "release directory not found: $dir" >&2
	exit 1
fi

if ! command -v cosign >/dev/null 2>&1; then
	echo "cosign is required to sign release artifacts" >&2
	exit 1
fi

found=0
for artifact in "$dir"/kronos-*; do
	[ -f "$artifact" ] || continue
	case "$artifact" in
		*.sha256 | *.sig | *.pem | *.bundle | *.tmp) continue ;;
	esac
	found=1
	cosign sign-blob --yes \
		--bundle "$artifact.bundle" \
		"$artifact"
	if [ ! -s "$artifact.bundle" ]; then
		echo "missing cosign output for $artifact" >&2
		exit 1
	fi
	echo "$artifact: signed"
done

if [ "$found" -eq 0 ]; then
	echo "no release payloads found in $dir" >&2
	exit 1
fi

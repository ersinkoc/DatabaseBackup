#!/usr/bin/env sh
set -eu

dir="${1:-bin}"

if [ ! -d "$dir" ]; then
	echo "release directory not found: $dir" >&2
	exit 1
fi

found_binary=0
for artifact in "$dir"/kronos-*; do
	[ -f "$artifact" ] || continue
	case "$artifact" in
		*.sha256 | *.json | *.sig | *.pem | *.bundle | *.tmp) continue ;;
	esac
	found_binary=1
	checksum="$artifact.sha256"
	if [ ! -f "$checksum" ]; then
		echo "missing checksum for $artifact" >&2
		exit 1
	fi
	expected="$(awk '{print $1}' "$checksum")"
	if command -v sha256sum >/dev/null 2>&1; then
		actual="$(sha256sum "$artifact" | awk '{print $1}')"
	elif command -v shasum >/dev/null 2>&1; then
		actual="$(shasum -a 256 "$artifact" | awk '{print $1}')"
	else
		echo "sha256sum or shasum is required to verify release artifacts" >&2
		exit 1
	fi
	if [ "$expected" != "$actual" ]; then
		echo "checksum mismatch for $artifact" >&2
		exit 1
	fi
	echo "$artifact: OK"
done

if [ "$found_binary" -eq 0 ]; then
	echo "no kronos binaries found in $dir" >&2
	exit 1
fi

for required in kronos-provenance.json kronos-sbom.json; do
	if [ ! -s "$dir/$required" ]; then
		echo "missing release metadata: $dir/$required" >&2
		exit 1
	fi
done

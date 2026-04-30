#!/usr/bin/env sh
set -eu

usage() {
	echo "usage: $0 <evidence-dir>" >&2
	echo "verifies archived release evidence logs and summary metadata" >&2
}

evidence_dir="${1:-}"
if [ -z "$evidence_dir" ]; then
	usage
	exit 2
fi

summary="$evidence_dir/summary.txt"
if [ ! -d "$evidence_dir" ]; then
	echo "release evidence directory not found: $evidence_dir" >&2
	exit 1
fi
if [ ! -s "$summary" ]; then
	echo "release evidence summary missing or empty: $summary" >&2
	exit 1
fi

summary_value() {
	key="$1"
	sed -n "s/^$key=//p" "$summary" | tail -n 1
}

require_summary_key() {
	key="$1"
	value="$(summary_value "$key")"
	if [ -z "$value" ]; then
		echo "release evidence summary missing key: $key" >&2
		exit 1
	fi
	printf '%s\n' "$value"
}

require_nonempty_log() {
	key="$1"
	path="$evidence_dir/$(require_summary_key "$key")"
	if [ ! -s "$path" ]; then
		echo "release evidence log missing or empty: $path" >&2
		exit 1
	fi
}

release_tag="$(require_summary_key release_tag)"
require_summary_key release_dir >/dev/null
require_summary_key evidence_dir >/dev/null
require_summary_key git_commit >/dev/null
require_summary_key verified_at >/dev/null

require_nonempty_log checksum_log
require_nonempty_log signature_log
require_nonempty_log tag_signature_log
require_nonempty_log attestation_log
require_nonempty_log digests

tag_log="$evidence_dir/$(require_summary_key tag_signature_log)"
if [ "$release_tag" != "unknown" ] &&
	grep -q "release tag signature verification not run" "$tag_log"; then
	echo "release tag signature evidence was not captured for $release_tag" >&2
	exit 1
fi

attestation_log="$evidence_dir/$(require_summary_key attestation_log)"
if [ "${KRONOS_REQUIRE_ATTESTATIONS:-0}" = "1" ] &&
	grep -q "attestation verification not run" "$attestation_log"; then
	echo "release attestation evidence was required but not captured" >&2
	exit 1
fi

echo "release evidence verified: $evidence_dir"

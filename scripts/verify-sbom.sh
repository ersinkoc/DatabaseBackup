#!/usr/bin/env sh
set -eu

dir="${1:-bin}"
sbom="$dir/kronos-sbom.json"
go_cmd="${GO:-go}"
govulncheck_cmd="${GOVULNCHECK:-govulncheck}"
require_govulncheck="${KRONOS_REQUIRE_GOVULNCHECK:-0}"
go_path_dir=""

case "$go_cmd" in
*/*)
	go_path_dir="$(CDPATH= cd "$(dirname "$go_cmd")" && pwd)"
	;;
esac

if [ ! -s "$sbom" ]; then
	echo "missing SBOM: $sbom" >&2
	exit 1
fi

require_text() {
	pattern="$1"
	description="$2"
	if ! grep -F "$pattern" "$sbom" >/dev/null 2>&1; then
		echo "SBOM $sbom missing $description" >&2
		exit 1
	fi
}

require_text '"spdxVersion": "SPDX-2.3"' "SPDX format marker"
require_text '"packages": [' "package list"

missing="$(mktemp)"
trap 'rm -f "$missing"' EXIT HUP INT TERM

"$go_cmd" list -m all | while read -r module version; do
	if [ -z "$module" ]; then
		continue
	fi
	if ! grep -F "\"name\": \"$module\"" "$sbom" >/dev/null 2>&1; then
		echo "missing SBOM component for Go module: $module" >>"$missing"
	fi
	if [ -n "${version:-}" ] && ! grep -F "\"versionInfo\": \"$version\"" "$sbom" >/dev/null 2>&1; then
		echo "missing SBOM version $version for Go module: $module" >>"$missing"
	fi
done

if [ -s "$missing" ]; then
	cat "$missing" >&2
	exit 1
fi

echo "SBOM module graph verified: $sbom"

if command -v "$govulncheck_cmd" >/dev/null 2>&1; then
	if [ -n "$go_path_dir" ]; then
		PATH="$go_path_dir:$PATH" "$govulncheck_cmd" ./...
	else
		"$govulncheck_cmd" ./...
	fi
elif [ "$require_govulncheck" = "1" ]; then
	echo "govulncheck is required but was not found" >&2
	exit 1
else
	echo "govulncheck not installed; skipping vulnerability scan"
fi

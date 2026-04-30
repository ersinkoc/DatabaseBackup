#!/usr/bin/env sh
set -eu

go_cmd="${GO:-go}"
version="${VERSION:-dev}"
commit="${COMMIT:-unknown}"
build_date="${BUILD_DATE:-$(date -u +%Y-%m-%dT%H:%M:%SZ)}"
out="${SBOM_OUT:-bin/kronos-sbom.json}"

mkdir -p "$(dirname "$out")"

tmp="$out.tmp"
{
	printf '{\n'
	printf '  "spdxVersion": "SPDX-2.3",\n'
	printf '  "dataLicense": "CC0-1.0",\n'
	printf '  "SPDXID": "SPDXRef-DOCUMENT",\n'
	printf '  "name": "kronos-%s",\n' "$version"
	printf '  "documentNamespace": "https://github.com/ersinkoc/Kronos/sbom/%s/%s",\n' "$version" "$commit"
	printf '  "creationInfo": {\n'
	printf '    "created": "%s",\n' "$build_date"
	printf '    "creators": ["Tool: scripts/sbom.sh"]\n'
	printf '  },\n'
	printf '  "packages": [\n'
	first=1
	index=0
	"$go_cmd" list -m all | while read -r path module_version; do
		[ -n "$path" ] || continue
		if [ "$first" -eq 0 ]; then
			printf ',\n'
		fi
		first=0
		index=$((index + 1))
		spdx_id="$(printf '%s' "$path" | tr -c 'A-Za-z0-9.' '-')"
		if [ -n "${module_version:-}" ]; then
			printf '    {"name": "%s", "SPDXID": "SPDXRef-Package-%s-%s", "versionInfo": "%s", "downloadLocation": "NOASSERTION", "filesAnalyzed": false, "supplier": "NOASSERTION"}' "$path" "$index" "$spdx_id" "$module_version"
		else
			printf '    {"name": "%s", "SPDXID": "SPDXRef-Package-%s-%s", "versionInfo": "%s", "downloadLocation": "NOASSERTION", "filesAnalyzed": false, "supplier": "NOASSERTION"}' "$path" "$index" "$spdx_id" "$version"
		fi
	done
	printf '\n  ]\n'
	printf '}\n'
} >"$tmp"
mv "$tmp" "$out"
echo "$out"

#!/usr/bin/env sh
set -eu

version="${VERSION:-dev}"
commit="${COMMIT:-unknown}"
build_date="${BUILD_DATE:-$(date -u +%Y-%m-%dT%H:%M:%SZ)}"
builder="${BUILDER:-local}"
out="${PROVENANCE_OUT:-bin/kronos-provenance.json}"

mkdir -p "$(dirname "$out")"

tmp="$out.tmp"
{
	printf '{\n'
	printf '  "subject": "kronos",\n'
	printf '  "version": "%s",\n' "$version"
	printf '  "commit": "%s",\n' "$commit"
	printf '  "buildDate": "%s",\n' "$build_date"
	printf '  "builder": "%s",\n' "$builder"
	printf '  "artifacts": [\n'
	first=1
	for artifact in bin/kronos-*; do
		case "$artifact" in
			*.sha256 | *.json | *.tmp) continue ;;
		esac
		[ -f "$artifact" ] || continue
		if command -v sha256sum >/dev/null 2>&1; then
			sha="$(sha256sum "$artifact" | awk '{print $1}')"
		elif command -v shasum >/dev/null 2>&1; then
			sha="$(shasum -a 256 "$artifact" | awk '{print $1}')"
		else
			echo "sha256sum or shasum is required to write provenance" >&2
			exit 1
		fi
		if [ "$first" -eq 0 ]; then
			printf ',\n'
		fi
		first=0
		size="$(wc -c <"$artifact" | tr -d ' ')"
		printf '    {"name": "%s", "sha256": "%s", "size": %s}' "$(basename "$artifact")" "$sha" "$size"
	done
	printf '\n  ]\n'
	printf '}\n'
} >"$tmp"
mv "$tmp" "$out"
echo "$out"

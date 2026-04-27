#!/usr/bin/env sh
set -eu

dir="${1:-bin}"
go_cmd="${GO:-go}"

if [ ! -d "$dir" ]; then
	echo "release directory not found: $dir" >&2
	exit 1
fi

if command -v "$go_cmd" >/dev/null 2>&1; then
	goos="$("$go_cmd" env GOOS)"
	goarch="$("$go_cmd" env GOARCH)"
else
	case "$(uname -s)" in
		Linux) goos="linux" ;;
		Darwin) goos="darwin" ;;
		*) echo "cannot infer host GOOS; set GO or install go" >&2; exit 1 ;;
	esac
	case "$(uname -m)" in
		x86_64 | amd64) goarch="amd64" ;;
		aarch64 | arm64) goarch="arm64" ;;
		*) echo "cannot infer host GOARCH; set GO or install go" >&2; exit 1 ;;
	esac
fi

artifact="$dir/kronos-$goos-$goarch"
if [ ! -f "$artifact" ]; then
	echo "host release artifact not found: $artifact" >&2
	exit 1
fi
if [ ! -x "$artifact" ]; then
	echo "host release artifact is not executable: $artifact" >&2
	exit 1
fi

version_output="$("$artifact" version)"
printf '%s\n' "$version_output" | grep '^kronos ' >/dev/null
printf '%s\n' "$version_output" | grep '^commit: ' >/dev/null
printf '%s\n' "$version_output" | grep '^built: ' >/dev/null

"$artifact" completion bash | bash -n

echo "$artifact: smoke OK"

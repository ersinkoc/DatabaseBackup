#!/usr/bin/env sh
set -eu

go_cmd="${GO:-go}"
target="${1:-$("$go_cmd" env GOOS)/$("$go_cmd" env GOARCH)}"
goos="${target%/*}"
goarch="${target#*/}"

if [ "$goos" = "$goarch" ]; then
	echo "usage: $0 <goos>/<goarch>" >&2
	exit 2
fi

version="${VERSION:-dev}"
commit="${COMMIT:-unknown}"
build_date="${BUILD_DATE:-$(date -u +%Y-%m-%dT%H:%M:%SZ)}"
out="bin/kronos-${goos}-${goarch}"

mkdir -p bin
CGO_ENABLED=0 GOOS="$goos" GOARCH="$goarch" "$go_cmd" build -trimpath \
	-ldflags "-s -w -X github.com/kronos/kronos/internal/buildinfo.Version=$version -X github.com/kronos/kronos/internal/buildinfo.Commit=$commit -X github.com/kronos/kronos/internal/buildinfo.BuildDate=$build_date" \
	-o "$out" ./cmd/kronos

echo "$out"

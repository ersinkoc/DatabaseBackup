#!/usr/bin/env sh
set -eu

go_cmd="${GO:-go}"
if [ -n "${GOFMT:-}" ]; then
	gofmt_cmd="$GOFMT"
else
	go_bin_dir="$(dirname "$go_cmd")"
	if [ -x "$go_bin_dir/gofmt" ]; then
		gofmt_cmd="$go_bin_dir/gofmt"
	else
		gofmt_cmd="gofmt"
	fi
fi
bin="${BIN:-bin/kronos}"

go_files="$(find . -name '*.go' -not -path './.git/*' -not -path './.tools/*' -not -path './bin/*')"
unformatted="$($gofmt_cmd -l -s $go_files)"
if [ -n "$unformatted" ]; then
	echo "$unformatted"
	echo "gofmt required; run $gofmt_cmd -w -s <files>" >&2
	exit 1
fi

"$go_cmd" vet ./...
"$go_cmd" test ./...

version="${VERSION:-$(git describe --tags --always --dirty 2>/dev/null || echo dev)}"
commit="${COMMIT:-$(git rev-parse --short HEAD 2>/dev/null || echo unknown)}"
build_date="${BUILD_DATE:-$(date -u +%Y-%m-%dT%H:%M:%SZ)}"

mkdir -p "$(dirname "$bin")"
CGO_ENABLED=0 "$go_cmd" build -trimpath \
	-ldflags "-s -w -X github.com/kronos/kronos/internal/buildinfo.Version=$version -X github.com/kronos/kronos/internal/buildinfo.Commit=$commit -X github.com/kronos/kronos/internal/buildinfo.BuildDate=$build_date" \
	-o "$bin" ./cmd/kronos

sh -n scripts/build.sh
sh -n scripts/release.sh
sh -n scripts/provenance.sh
sh -n scripts/sbom.sh
sh -n scripts/verify-release.sh
sh -n scripts/smoke-release.sh
sh -n scripts/production-check.sh
sh -n web/build.sh

"$bin" completion bash | bash -n
"$bin" version >/dev/null

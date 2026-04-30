#!/usr/bin/env sh
set -eu

usage() {
	echo "usage: $0 <release-tag>" >&2
	echo "fetches and verifies a signed release tag from origin" >&2
}

tag="${1:-}"
if [ -z "$tag" ]; then
	usage
	exit 2
fi

if ! command -v git >/dev/null 2>&1; then
	echo "git is required to verify release tags" >&2
	exit 1
fi

if ! git rev-parse --git-dir >/dev/null 2>&1; then
	echo "release tag verification must run inside a Git repository" >&2
	exit 1
fi

if ! git ls-remote --exit-code --tags origin "refs/tags/$tag" >/dev/null 2>&1; then
	echo "release tag not found on origin: $tag" >&2
	exit 1
fi

if ! git rev-parse -q --verify "refs/tags/$tag" >/dev/null 2>&1; then
	git fetch --tags origin "refs/tags/$tag:refs/tags/$tag"
fi

git verify-tag "$tag"
echo "release tag signature verified: $tag"

#!/usr/bin/env sh
set -eu

usage() {
	echo "usage: $0 <release-tag>" >&2
	echo "checks local GPG/Git signing readiness without pushing a tag" >&2
}

tag="${1:-}"
if [ -z "$tag" ]; then
	usage
	exit 2
fi

if ! command -v git >/dev/null 2>&1; then
	echo "git is required for release signing checks" >&2
	exit 1
fi
if ! command -v gpg >/dev/null 2>&1; then
	echo "gpg is required for signed release tags" >&2
	exit 1
fi

if ! git rev-parse --git-dir >/dev/null 2>&1; then
	echo "release signing check must run inside a Git repository" >&2
	exit 1
fi

signing_key="$(git config --get user.signingkey || true)"
if [ -z "$signing_key" ]; then
	echo "git config user.signingkey is required for signed release tags" >&2
	exit 1
fi

if ! gpg --list-secret-keys "$signing_key" >/dev/null 2>&1; then
	echo "gpg secret key not found for user.signingkey=$signing_key" >&2
	exit 1
fi

if git rev-parse -q --verify "refs/tags/$tag" >/dev/null 2>&1; then
	echo "release tag already exists locally: $tag" >&2
	exit 1
fi

if git ls-remote --exit-code --tags origin "refs/tags/$tag" >/dev/null 2>&1; then
	echo "release tag already exists on origin: $tag" >&2
	exit 1
fi

probe="kronos-signing-probe-$(date -u +%Y%m%d%H%M%S)-$$"
cleanup() {
	git tag -d "$probe" >/dev/null 2>&1 || true
}
trap cleanup EXIT HUP INT TERM

if ! git tag -s "$probe" -m "$probe" HEAD >/dev/null 2>&1; then
	echo "failed to create a temporary signed tag with user.signingkey=$signing_key" >&2
	exit 1
fi

if ! git verify-tag "$probe" >/dev/null 2>&1; then
	echo "temporary signed tag verification failed" >&2
	exit 1
fi

echo "release signing preflight OK for $tag using user.signingkey=$signing_key"

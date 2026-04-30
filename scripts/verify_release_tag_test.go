package scripts_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestVerifyReleaseTagFetchesAndVerifiesRemoteTag(t *testing.T) {
	t.Parallel()

	root := filepath.Dir(mustGetwd(t))
	binDir := t.TempDir()
	callsPath := filepath.Join(t.TempDir(), "git.calls")
	fakeGit := filepath.Join(binDir, "git")
	gitScript := `#!/usr/bin/env sh
set -eu
printf '%s\n' "$*" >> "$GIT_CALLS"
case "$1" in
	rev-parse)
		if [ "$2" = "--git-dir" ]; then
			printf '%s\n' .git
			exit 0
		fi
		if [ "$2" = "-q" ] && [ "$3" = "--verify" ]; then
			exit 1
		fi
		;;
	ls-remote)
		exit 0
		;;
	fetch)
		exit 0
		;;
	verify-tag)
		exit 0
		;;
esac
echo "unexpected git invocation: $*" >&2
exit 1
`
	if err := os.WriteFile(fakeGit, []byte(gitScript), 0o755); err != nil {
		t.Fatalf("WriteFile(fake git) error = %v", err)
	}

	cmd := exec.Command("sh", filepath.Join(root, "scripts", "verify-release-tag.sh"), "v0.0.0-test")
	cmd.Dir = root
	cmd.Env = append(cleanEnv(os.Environ(), "GIT_CALLS"),
		"PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"),
		"GIT_CALLS="+callsPath,
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("verify-release-tag.sh error = %v\n%s", err, output)
	}
	if !strings.Contains(string(output), "release tag signature verified: v0.0.0-test") {
		t.Fatalf("unexpected output:\n%s", output)
	}
	calls, err := os.ReadFile(callsPath)
	if err != nil {
		t.Fatalf("ReadFile(git calls) error = %v", err)
	}
	for _, want := range []string{
		"ls-remote --exit-code --tags origin refs/tags/v0.0.0-test",
		"fetch --tags origin refs/tags/v0.0.0-test:refs/tags/v0.0.0-test",
		"verify-tag v0.0.0-test",
	} {
		if !strings.Contains(string(calls), want) {
			t.Fatalf("git calls missing %q:\n%s", want, calls)
		}
	}
}

func TestVerifyReleaseTagRequiresRemoteTag(t *testing.T) {
	t.Parallel()

	root := filepath.Dir(mustGetwd(t))
	binDir := t.TempDir()
	fakeGit := filepath.Join(binDir, "git")
	gitScript := `#!/usr/bin/env sh
set -eu
case "$1" in
	rev-parse)
		if [ "$2" = "--git-dir" ]; then
			printf '%s\n' .git
			exit 0
		fi
		;;
	ls-remote)
		exit 1
		;;
esac
echo "unexpected git invocation: $*" >&2
exit 1
`
	if err := os.WriteFile(fakeGit, []byte(gitScript), 0o755); err != nil {
		t.Fatalf("WriteFile(fake git) error = %v", err)
	}

	cmd := exec.Command("sh", filepath.Join(root, "scripts", "verify-release-tag.sh"), "v0.0.0-missing")
	cmd.Dir = root
	cmd.Env = append(cleanEnv(os.Environ()),
		"PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"),
	)
	output, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("verify-release-tag.sh error = nil, want failure\n%s", output)
	}
	if !strings.Contains(string(output), "release tag not found on origin: v0.0.0-missing") {
		t.Fatalf("unexpected output:\n%s", output)
	}
}

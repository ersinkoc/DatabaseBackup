package scripts_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCheckReleaseSigningPreflight(t *testing.T) {
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
	config)
		if [ "$2" = "--get" ] && [ "$3" = "user.signingkey" ]; then
			printf '%s\n' ABC123
			exit 0
		fi
		;;
	ls-remote)
		exit 1
		;;
	tag)
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
	fakeGPG := filepath.Join(binDir, "gpg")
	if err := os.WriteFile(fakeGPG, []byte("#!/usr/bin/env sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("WriteFile(fake gpg) error = %v", err)
	}

	cmd := exec.Command("sh", filepath.Join(root, "scripts", "check-release-signing.sh"), "v0.0.0-test")
	cmd.Dir = root
	cmd.Env = append(cleanEnv(os.Environ(), "GIT_CALLS"),
		"PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"),
		"GIT_CALLS="+callsPath,
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("check-release-signing.sh error = %v\n%s", err, output)
	}
	if !strings.Contains(string(output), "release signing preflight OK for v0.0.0-test") {
		t.Fatalf("unexpected output:\n%s", output)
	}
	calls, err := os.ReadFile(callsPath)
	if err != nil {
		t.Fatalf("ReadFile(git calls) error = %v", err)
	}
	for _, want := range []string{
		"config --get user.signingkey",
		"ls-remote --exit-code --tags origin refs/tags/v0.0.0-test",
		"tag -s kronos-signing-probe-",
		"verify-tag kronos-signing-probe-",
		"tag -d kronos-signing-probe-",
	} {
		if !strings.Contains(string(calls), want) {
			t.Fatalf("git calls missing %q:\n%s", want, calls)
		}
	}
}

func TestCheckReleaseSigningRequiresSigningKey(t *testing.T) {
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
	config)
		if [ "$2" = "--get" ] && [ "$3" = "user.signingkey" ]; then
			exit 1
		fi
		;;
esac
echo "unexpected git invocation: $*" >&2
exit 1
`
	if err := os.WriteFile(fakeGit, []byte(gitScript), 0o755); err != nil {
		t.Fatalf("WriteFile(fake git) error = %v", err)
	}
	fakeGPG := filepath.Join(binDir, "gpg")
	if err := os.WriteFile(fakeGPG, []byte("#!/usr/bin/env sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("WriteFile(fake gpg) error = %v", err)
	}

	cmd := exec.Command("sh", filepath.Join(root, "scripts", "check-release-signing.sh"), "v0.0.0-test")
	cmd.Dir = root
	cmd.Env = append(cleanEnv(os.Environ()),
		"PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"),
	)
	output, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("check-release-signing.sh error = nil, want failure\n%s", output)
	}
	if !strings.Contains(string(output), "git config user.signingkey is required") {
		t.Fatalf("unexpected output:\n%s", output)
	}
}

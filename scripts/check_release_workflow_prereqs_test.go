package scripts_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCheckReleaseWorkflowPrereqsRequiresPublicKeySecret(t *testing.T) {
	t.Parallel()

	root := filepath.Dir(mustGetwd(t))
	binDir := t.TempDir()
	fakeGh := filepath.Join(binDir, "gh")
	ghScript := `#!/usr/bin/env sh
set -eu
if [ "$1" = "secret" ] && [ "$2" = "list" ]; then
	printf '%s\t%s\n' OTHER_SECRET 2026-04-30
	exit 0
fi
echo "unexpected gh invocation: $*" >&2
exit 1
`
	if err := os.WriteFile(fakeGh, []byte(ghScript), 0o755); err != nil {
		t.Fatalf("WriteFile(fake gh) error = %v", err)
	}

	cmd := exec.Command("sh", filepath.Join(root, "scripts", "check-release-workflow-prereqs.sh"))
	cmd.Dir = root
	cmd.Env = append(cleanEnv(os.Environ(), "GH_RELEASE_REPO", "GITHUB_REPOSITORY", "KRONOS_RELEASE_TAG_PUBLIC_KEY_SECRET"),
		"PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"),
		"GH_RELEASE_REPO=ersinkoc/Kronos",
	)
	output, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("check-release-workflow-prereqs.sh error = nil, want failure\n%s", output)
	}
	if !strings.Contains(string(output), "GitHub secret missing for tagged releases: KRONOS_RELEASE_TAG_PUBLIC_KEY in ersinkoc/Kronos") {
		t.Fatalf("missing secret error not found:\n%s", output)
	}
}

func TestCheckReleaseWorkflowPrereqsPassesWhenSecretExists(t *testing.T) {
	t.Parallel()

	root := filepath.Dir(mustGetwd(t))
	binDir := t.TempDir()
	fakeGh := filepath.Join(binDir, "gh")
	ghScript := `#!/usr/bin/env sh
set -eu
if [ "$1" = "secret" ] && [ "$2" = "list" ]; then
	printf '%s\t%s\n' KRONOS_RELEASE_TAG_PUBLIC_KEY 2026-04-30
	exit 0
fi
echo "unexpected gh invocation: $*" >&2
exit 1
`
	if err := os.WriteFile(fakeGh, []byte(ghScript), 0o755); err != nil {
		t.Fatalf("WriteFile(fake gh) error = %v", err)
	}

	cmd := exec.Command("sh", filepath.Join(root, "scripts", "check-release-workflow-prereqs.sh"))
	cmd.Dir = root
	cmd.Env = append(cleanEnv(os.Environ(), "GH_RELEASE_REPO", "GITHUB_REPOSITORY", "KRONOS_RELEASE_TAG_PUBLIC_KEY_SECRET"),
		"PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"),
		"GH_RELEASE_REPO=ersinkoc/Kronos",
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("check-release-workflow-prereqs.sh error = %v\n%s", err, output)
	}
	if !strings.Contains(string(output), "release workflow prerequisites OK for ersinkoc/Kronos") {
		t.Fatalf("success output missing:\n%s", output)
	}
}

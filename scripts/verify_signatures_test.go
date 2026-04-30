package scripts_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestVerifySignaturesDefaultsToProjectWorkflowIdentity(t *testing.T) {
	t.Parallel()

	root := filepath.Dir(mustGetwd(t))
	dir := t.TempDir()
	artifact := filepath.Join(dir, "kronos-linux-amd64")
	for _, path := range []string{artifact, artifact + ".bundle"} {
		if err := os.WriteFile(path, []byte("payload\n"), 0o755); err != nil {
			t.Fatalf("WriteFile(%s) error = %v", path, err)
		}
	}

	binDir := t.TempDir()
	argsPath := filepath.Join(t.TempDir(), "cosign.args")
	fakeCosign := filepath.Join(binDir, "cosign")
	fake := "#!/usr/bin/env sh\n" +
		"printf '%s\\n' \"$@\" > \"$COSIGN_ARGS_OUT\"\n"
	if err := os.WriteFile(fakeCosign, []byte(fake), 0o755); err != nil {
		t.Fatalf("WriteFile(fake cosign) error = %v", err)
	}

	cmd := exec.Command("sh", filepath.Join(root, "scripts", "verify-signatures.sh"), dir)
	cmd.Dir = root
	cmd.Env = append(cleanEnv(os.Environ(), "GITHUB_REPOSITORY", "COSIGN_CERTIFICATE_IDENTITY_REGEXP"),
		"PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"),
		"COSIGN_ARGS_OUT="+argsPath,
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("verify-signatures.sh error = %v\n%s", err, output)
	}

	data, err := os.ReadFile(argsPath)
	if err != nil {
		t.Fatalf("ReadFile(cosign args) error = %v", err)
	}
	args := string(data)
	if !strings.Contains(args, "--certificate-identity-regexp\nhttps://github.com/ersinkoc/Kronos/.github/workflows/release.yml@.*") {
		t.Fatalf("cosign args did not include strict project identity regexp:\n%s", args)
	}
}

func cleanEnv(env []string, keys ...string) []string {
	blocked := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		blocked[key] = struct{}{}
	}
	out := env[:0]
	for _, item := range env {
		key, _, ok := strings.Cut(item, "=")
		if !ok {
			continue
		}
		if _, skip := blocked[key]; skip {
			continue
		}
		out = append(out, item)
	}
	return out
}

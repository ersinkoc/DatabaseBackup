package scripts_test

import (
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestArchiveReleaseEvidenceCapturesVerificationLogs(t *testing.T) {
	t.Parallel()

	root := filepath.Dir(mustGetwd(t))
	releaseDir := t.TempDir()
	evidenceDir := filepath.Join(t.TempDir(), "evidence")
	artifact := filepath.Join(releaseDir, "kronos-linux-amd64")
	content := []byte("test release artifact\n")
	if err := os.WriteFile(artifact, content, 0o755); err != nil {
		t.Fatalf("WriteFile(artifact) error = %v", err)
	}
	sum := sha256.Sum256(content)
	checksum := fmt.Sprintf("%x  bin/kronos-linux-amd64\n", sum)
	if err := os.WriteFile(artifact+".sha256", []byte(checksum), 0o644); err != nil {
		t.Fatalf("WriteFile(checksum) error = %v", err)
	}
	for _, path := range []string{
		artifact + ".sig",
		artifact + ".pem",
		filepath.Join(releaseDir, "kronos-provenance.json"),
		filepath.Join(releaseDir, "kronos-provenance.json.sig"),
		filepath.Join(releaseDir, "kronos-provenance.json.pem"),
		filepath.Join(releaseDir, "kronos-sbom.json"),
		filepath.Join(releaseDir, "kronos-sbom.json.sig"),
		filepath.Join(releaseDir, "kronos-sbom.json.pem"),
	} {
		if err := os.WriteFile(path, []byte("payload\n"), 0o644); err != nil {
			t.Fatalf("WriteFile(%s) error = %v", path, err)
		}
	}

	binDir := t.TempDir()
	fakeCosign := filepath.Join(binDir, "cosign")
	fake := "#!/usr/bin/env sh\n" +
		"echo cosign \"$@\"\n"
	if err := os.WriteFile(fakeCosign, []byte(fake), 0o755); err != nil {
		t.Fatalf("WriteFile(fake cosign) error = %v", err)
	}

	cmd := exec.Command("sh", filepath.Join(root, "scripts", "archive-release-evidence.sh"), releaseDir, evidenceDir)
	cmd.Dir = root
	cmd.Env = append(cleanEnv(os.Environ(), "GH_ATTESTATION_REPO", "GITHUB_REPOSITORY", "COSIGN_CERTIFICATE_IDENTITY_REGEXP"),
		"PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"),
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("archive-release-evidence.sh error = %v\n%s", err, output)
	}

	for _, name := range []string{"checksum.log", "signatures.log", "attestations.log", "artifact-digests.txt", "summary.txt"} {
		path := filepath.Join(evidenceDir, name)
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("ReadFile(%s) error = %v", path, err)
		}
		if len(data) == 0 {
			t.Fatalf("%s is empty", path)
		}
	}
	summary, err := os.ReadFile(filepath.Join(evidenceDir, "summary.txt"))
	if err != nil {
		t.Fatalf("ReadFile(summary) error = %v", err)
	}
	if !strings.Contains(string(summary), "checksum_log=checksum.log") ||
		!strings.Contains(string(summary), "signature_log=signatures.log") ||
		!strings.Contains(string(summary), "attestation_log=attestations.log") {
		t.Fatalf("summary missing expected log references:\n%s", summary)
	}
}

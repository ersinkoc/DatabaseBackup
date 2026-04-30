package scripts_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestVerifyReleaseEvidenceAcceptsCompleteArchive(t *testing.T) {
	t.Parallel()

	evidenceDir := writeEvidenceArchive(t, "v0.0.0-test", map[string]string{
		"checksum.log":         "checksums OK\n",
		"signatures.log":       "signatures OK\n",
		"tag-signature.log":    "release tag signature verified: v0.0.0-test\n",
		"attestations.log":     "attestations OK\n",
		"artifact-digests.txt": "abc123  kronos-linux-amd64\n",
	})

	output, err := runVerifyReleaseEvidence(t, evidenceDir, nil)
	if err != nil {
		t.Fatalf("verify-release-evidence.sh error = %v\n%s", err, output)
	}
	if !strings.Contains(string(output), "release evidence verified: "+evidenceDir) {
		t.Fatalf("unexpected output:\n%s", output)
	}
}

func TestVerifyReleaseEvidenceRejectsMissingTagSignatureForTaggedRelease(t *testing.T) {
	t.Parallel()

	evidenceDir := writeEvidenceArchive(t, "v0.0.0-test", map[string]string{
		"checksum.log":         "checksums OK\n",
		"signatures.log":       "signatures OK\n",
		"tag-signature.log":    "KRONOS_RELEASE_TAG not set; release tag signature verification not run.\n",
		"attestations.log":     "attestations OK\n",
		"artifact-digests.txt": "abc123  kronos-linux-amd64\n",
	})

	output, err := runVerifyReleaseEvidence(t, evidenceDir, nil)
	if err == nil {
		t.Fatalf("verify-release-evidence.sh error = nil, want failure\n%s", output)
	}
	if !strings.Contains(string(output), "release tag signature evidence was not captured for v0.0.0-test") {
		t.Fatalf("unexpected output:\n%s", output)
	}
}

func TestVerifyReleaseEvidenceCanRequireAttestations(t *testing.T) {
	t.Parallel()

	evidenceDir := writeEvidenceArchive(t, "v0.0.0-test", map[string]string{
		"checksum.log":         "checksums OK\n",
		"signatures.log":       "signatures OK\n",
		"tag-signature.log":    "release tag signature verified: v0.0.0-test\n",
		"attestations.log":     "GH_ATTESTATION_REPO not set; attestation verification not run.\n",
		"artifact-digests.txt": "abc123  kronos-linux-amd64\n",
	})

	output, err := runVerifyReleaseEvidence(t, evidenceDir, []string{"KRONOS_REQUIRE_ATTESTATIONS=1"})
	if err == nil {
		t.Fatalf("verify-release-evidence.sh error = nil, want failure\n%s", output)
	}
	if !strings.Contains(string(output), "release attestation evidence was required but not captured") {
		t.Fatalf("unexpected output:\n%s", output)
	}
}

func writeEvidenceArchive(t *testing.T, releaseTag string, files map[string]string) string {
	t.Helper()

	evidenceDir := t.TempDir()
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(evidenceDir, name), []byte(content), 0o644); err != nil {
			t.Fatalf("WriteFile(%s) error = %v", name, err)
		}
	}

	summary := strings.Join([]string{
		"release_tag=" + releaseTag,
		"release_dir=bin",
		"evidence_dir=" + evidenceDir,
		"git_commit=abc1234",
		"verified_at=2026-04-30T00:00:00Z",
		"checksum_log=checksum.log",
		"signature_log=signatures.log",
		"tag_signature_log=tag-signature.log",
		"attestation_log=attestations.log",
		"digests=artifact-digests.txt",
		"",
	}, "\n")
	if err := os.WriteFile(filepath.Join(evidenceDir, "summary.txt"), []byte(summary), 0o644); err != nil {
		t.Fatalf("WriteFile(summary) error = %v", err)
	}
	return evidenceDir
}

func runVerifyReleaseEvidence(t *testing.T, evidenceDir string, extraEnv []string) ([]byte, error) {
	t.Helper()

	root := filepath.Dir(mustGetwd(t))
	cmd := exec.Command("sh", filepath.Join(root, "scripts", "verify-release-evidence.sh"), evidenceDir)
	cmd.Dir = root
	cmd.Env = append(cleanEnv(os.Environ(), "KRONOS_REQUIRE_ATTESTATIONS"), extraEnv...)
	return cmd.CombinedOutput()
}

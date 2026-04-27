package docs_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var markdownLink = regexp.MustCompile(`\[[^\]]+\]\(([^)]+)\)`)

func TestLocalMarkdownLinksResolve(t *testing.T) {
	t.Parallel()

	root := ".."
	for _, path := range markdownFiles(t, root) {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("ReadFile(%s) error = %v", path, err)
		}
		for _, match := range markdownLink.FindAllStringSubmatch(string(data), -1) {
			target := strings.TrimSpace(match[1])
			if shouldSkipLink(target) {
				continue
			}
			target = strings.Trim(target, "<>")
			if hash := strings.IndexByte(target, '#'); hash >= 0 {
				target = target[:hash]
			}
			if target == "" {
				continue
			}
			resolved := filepath.Clean(filepath.Join(filepath.Dir(path), target))
			if _, err := os.Stat(resolved); err != nil {
				t.Fatalf("%s links to missing local path %q", path, target)
			}
		}
	}
}

func TestCLIReferenceDocumentsRequestID(t *testing.T) {
	t.Parallel()

	data, err := os.ReadFile("cli.md")
	if err != nil {
		t.Fatalf("ReadFile(cli.md) error = %v", err)
	}
	text := string(data)
	for _, want := range []string{"--request-id", "X-Kronos-Request-ID"} {
		if !strings.Contains(text, want) {
			t.Fatalf("cli.md missing %q", want)
		}
	}
}

func TestKubernetesManifestsExist(t *testing.T) {
	t.Parallel()

	root := filepath.Join("..", "deploy", "kubernetes")
	for _, name := range []string{"namespace.yaml", "configmap.yaml", "pvc.yaml", "deployment.yaml", "service.yaml", "agent-deployment.yaml", "networkpolicy.yaml"} {
		path := filepath.Join(root, name)
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("ReadFile(%s) error = %v", path, err)
		}
		text := string(data)
		for _, want := range []string{"apiVersion:", "kind:", "metadata:"} {
			if !strings.Contains(text, want) {
				t.Fatalf("%s missing %q", path, want)
			}
		}
	}
}

func TestReleaseWorkflowPublishesArtifacts(t *testing.T) {
	t.Parallel()

	data, err := os.ReadFile(filepath.Join("..", ".github", "workflows", "release.yml"))
	if err != nil {
		t.Fatalf("ReadFile(release.yml) error = %v", err)
	}
	text := string(data)
	for _, want := range []string{
		"tags:",
		"./scripts/release.sh",
		"./scripts/provenance.sh",
		"./scripts/sbom.sh",
		"./scripts/sign-release.sh",
		"./scripts/verify-signatures.sh",
		"./scripts/verify-release.sh",
		"./scripts/smoke-release.sh",
		"attestations: write",
		"id-token: write",
		"actions/attest-build-provenance@v4.1.0",
		"actions/attest@v4.1.0",
		"sigstore/cosign-installer@v4.1.1",
		"actions/upload-artifact@v7",
		"gh release create",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("release.yml missing %q", want)
		}
	}
}

func TestReleaseScriptsIncludeProvenance(t *testing.T) {
	t.Parallel()

	for _, path := range []string{
		filepath.Join("..", "scripts", "provenance.sh"),
		filepath.Join("..", "scripts", "sbom.sh"),
		filepath.Join("..", "scripts", "sign-release.sh"),
		filepath.Join("..", "scripts", "verify-signatures.sh"),
		filepath.Join("..", "scripts", "verify-release.sh"),
		filepath.Join("..", "Makefile"),
	} {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("ReadFile(%s) error = %v", path, err)
		}
		text := string(data)
		if !strings.Contains(text, "provenance") && !strings.Contains(text, "sbom") && !strings.Contains(text, "SBOM") {
			t.Fatalf("%s does not mention release metadata", path)
		}
	}
}

func markdownFiles(t *testing.T, root string) []string {
	t.Helper()

	var paths []string
	for _, start := range []string{filepath.Join(root, "README.md"), "."} {
		info, err := os.Stat(start)
		if err != nil {
			t.Fatalf("Stat(%s) error = %v", start, err)
		}
		if !info.IsDir() {
			paths = append(paths, start)
			continue
		}
		err = filepath.WalkDir(start, func(path string, entry os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() {
				return nil
			}
			if strings.HasSuffix(entry.Name(), ".md") {
				paths = append(paths, path)
			}
			return nil
		})
		if err != nil {
			t.Fatalf("WalkDir(%s) error = %v", start, err)
		}
	}
	return paths
}

func shouldSkipLink(target string) bool {
	return strings.HasPrefix(target, "http://") ||
		strings.HasPrefix(target, "https://") ||
		strings.HasPrefix(target, "mailto:") ||
		strings.HasPrefix(target, "#")
}

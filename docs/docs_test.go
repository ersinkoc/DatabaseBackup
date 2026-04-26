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
	for _, name := range []string{"namespace.yaml", "configmap.yaml", "pvc.yaml", "deployment.yaml", "service.yaml"} {
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

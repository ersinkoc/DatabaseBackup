package scripts_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestVerifySBOMChecksModuleGraphAndRunsGovulncheck(t *testing.T) {
	t.Parallel()

	root := filepath.Dir(mustGetwd(t))
	dir := t.TempDir()
	out := filepath.Join(dir, "kronos-sbom.json")

	generate := exec.Command("sh", filepath.Join(root, "scripts", "sbom.sh"))
	generate.Dir = root
	generate.Env = append(os.Environ(),
		"GO="+testGoCommand(root),
		"SBOM_OUT="+out,
		"VERSION=test",
		"COMMIT=test",
		"BUILD_DATE=1970-01-01T00:00:00Z",
	)
	if output, err := generate.CombinedOutput(); err != nil {
		t.Fatalf("sbom.sh error = %v\n%s", err, output)
	}

	binDir := t.TempDir()
	argsOut := filepath.Join(t.TempDir(), "govulncheck-args")
	fakeGovulncheck := filepath.Join(binDir, "govulncheck")
	if err := os.WriteFile(fakeGovulncheck, []byte("#!/usr/bin/env sh\nprintf '%s\\n' \"$@\" > \"$GOVULNCHECK_ARGS_OUT\"\n"), 0o755); err != nil {
		t.Fatalf("WriteFile(fakeGovulncheck) error = %v", err)
	}

	cmd := exec.Command("sh", filepath.Join(root, "scripts", "verify-sbom.sh"), dir)
	cmd.Dir = root
	cmd.Env = append(os.Environ(),
		"GO="+testGoCommand(root),
		"GOVULNCHECK_ARGS_OUT="+argsOut,
		"PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"),
	)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("verify-sbom.sh error = %v\n%s", err, output)
	}

	args, err := os.ReadFile(argsOut)
	if err != nil {
		t.Fatalf("ReadFile(%s) error = %v", argsOut, err)
	}
	if strings.TrimSpace(string(args)) != "./..." {
		t.Fatalf("govulncheck args = %q, want ./...", strings.TrimSpace(string(args)))
	}
}

func TestVerifySBOMFailsWhenModuleIsMissing(t *testing.T) {
	t.Parallel()

	root := filepath.Dir(mustGetwd(t))
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "kronos-sbom.json"), []byte(`{
  "spdxVersion": "SPDX-2.3",
  "packages": []
}`), 0o644); err != nil {
		t.Fatalf("WriteFile(kronos-sbom.json) error = %v", err)
	}

	cmd := exec.Command("sh", filepath.Join(root, "scripts", "verify-sbom.sh"), dir)
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "GO="+testGoCommand(root))
	output, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("verify-sbom.sh succeeded unexpectedly\n%s", output)
	}
	if !strings.Contains(string(output), "missing SBOM component") {
		t.Fatalf("verify-sbom.sh output missing module failure:\n%s", output)
	}
}

func testGoCommand(root string) string {
	if goCommand := os.Getenv("GO"); goCommand != "" {
		return goCommand
	}
	local := filepath.Join(root, ".tools", "go", "bin", "go")
	if _, err := os.Stat(local); err == nil {
		return local
	}
	return "go"
}

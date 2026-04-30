package docs_test

import (
	"encoding/json"
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

func TestPublicDocsMatchDriverMVPStatus(t *testing.T) {
	t.Parallel()

	readme, err := os.ReadFile(filepath.Join("..", "README.md"))
	if err != nil {
		t.Fatalf("ReadFile(README.md) error = %v", err)
	}
	readmeText := string(readme)
	for _, want := range []string{
		"Redis/Valkey currently runs as the most complete native",
		"PostgreSQL, MySQL/MariaDB, and MongoDB are implemented as external-tool",
		"corresponding client tools installed",
		"Native protocol drivers and PITR remain",
	} {
		if !strings.Contains(readmeText, want) {
			t.Fatalf("README.md missing %q", want)
		}
	}
	if strings.Contains(readmeText, "Kronos is a zero-dependency Go binary") {
		t.Fatalf("README.md still markets the implemented MVP as zero dependency")
	}

	spec, err := os.ReadFile(filepath.Join("..", ".project", "SPECIFICATION.md"))
	if err != nil {
		t.Fatalf("ReadFile(SPECIFICATION.md) error = %v", err)
	}
	specText := string(spec)
	for _, want := range []string{
		"this document preserves the",
		"long-term product vision",
		"not an accurate MVP release contract",
		"external database tools for PostgreSQL, MySQL/MariaDB, and MongoDB",
		"docs/decisions/0002-external-tool-driver-mvp.md",
	} {
		if !strings.Contains(specText, want) {
			t.Fatalf(".project/SPECIFICATION.md missing %q", want)
		}
	}
}

func TestChangelogDocumentsUnreleasedScope(t *testing.T) {
	t.Parallel()

	changelog, err := os.ReadFile(filepath.Join("..", "CHANGELOG.md"))
	if err != nil {
		t.Fatalf("ReadFile(CHANGELOG.md) error = %v", err)
	}
	text := string(changelog)
	for _, want := range []string{
		"# Changelog",
		"## Unreleased",
		"### Added",
		"### Changed",
		"### Fixed",
		"Systemd unit examples",
		"Importable Grafana overview dashboard",
		"Release artifact SBOM module coverage",
		"external-tool MVP",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("CHANGELOG.md missing %q", want)
		}
	}

	readme, err := os.ReadFile(filepath.Join("..", "README.md"))
	if err != nil {
		t.Fatalf("ReadFile(README.md) error = %v", err)
	}
	if !strings.Contains(string(readme), "[Changelog](CHANGELOG.md)") {
		t.Fatalf("README.md missing changelog link")
	}
}

func TestKubernetesManifestsExist(t *testing.T) {
	t.Parallel()

	root := filepath.Join("..", "deploy", "kubernetes")
	for _, name := range []string{"namespace.yaml", "configmap.yaml", "pvc.yaml", "deployment.yaml", "service.yaml", "agent-deployment.yaml", "networkpolicy.yaml", "pdb.yaml"} {
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

func TestKubernetesControlPlaneDocumentsSingleReplicaBoundary(t *testing.T) {
	t.Parallel()

	read := func(path string) string {
		t.Helper()
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("ReadFile(%s) error = %v", path, err)
		}
		return string(data)
	}
	deployment := read(filepath.Join("..", "deploy", "kubernetes", "deployment.yaml"))
	for _, want := range []string{"replicas: 1", "type: Recreate", "claimName: kronos-state"} {
		if !strings.Contains(deployment, want) {
			t.Fatalf("deployment.yaml missing %q", want)
		}
	}
	pdb := read(filepath.Join("..", "deploy", "kubernetes", "pdb.yaml"))
	for _, want := range []string{"kind: PodDisruptionBudget", "minAvailable: 1"} {
		if !strings.Contains(pdb, want) {
			t.Fatalf("pdb.yaml missing %q", want)
		}
	}
	readme := read(filepath.Join("..", "deploy", "kubernetes", "README.md"))
	for _, want := range []string{"single-replica", "strategy: Recreate", "PodDisruptionBudget", "External Secrets Operator"} {
		if !strings.Contains(readme, want) {
			t.Fatalf("deploy/kubernetes/README.md missing %q", want)
		}
	}
}

func TestKubernetesManagedClusterOverlaysExist(t *testing.T) {
	t.Parallel()

	root := filepath.Join("..", "deploy", "kubernetes", "overlays")
	for _, provider := range []string{"eks", "gke", "aks"} {
		base := filepath.Join(root, provider)
		for _, name := range []string{"README.md", "kustomization.yaml", "pvc-storageclass.yaml", "serviceaccount.yaml", "workload-identity.yaml"} {
			path := filepath.Join(base, name)
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("ReadFile(%s) error = %v", path, err)
			}
			if len(data) == 0 {
				t.Fatalf("%s is empty", path)
			}
		}
	}
}

func TestKubernetesImmutableImageOverlayPinsDigest(t *testing.T) {
	t.Parallel()

	root := filepath.Join("..", "deploy", "kubernetes", "overlays", "immutable-image")
	for _, name := range []string{"README.md", "kustomization.yaml"} {
		path := filepath.Join(root, name)
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("ReadFile(%s) error = %v", path, err)
		}
		if len(data) == 0 {
			t.Fatalf("%s is empty", path)
		}
	}
	kustomization, err := os.ReadFile(filepath.Join(root, "kustomization.yaml"))
	if err != nil {
		t.Fatalf("ReadFile(immutable image kustomization) error = %v", err)
	}
	text := string(kustomization)
	for _, want := range []string{
		"images:",
		"name: ghcr.io/kronosbackup/kronos",
		"digest: sha256:",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("immutable image kustomization missing %q", want)
		}
	}
	readme, err := os.ReadFile(filepath.Join("..", "deploy", "kubernetes", "README.md"))
	if err != nil {
		t.Fatalf("ReadFile(deploy/kubernetes/README.md) error = %v", err)
	}
	if !strings.Contains(string(readme), "overlays/immutable-image") {
		t.Fatalf("deploy/kubernetes/README.md missing immutable-image overlay guidance")
	}
}

func TestSystemdUnitsExist(t *testing.T) {
	t.Parallel()

	root := filepath.Join("..", "contrib", "systemd")
	for _, name := range []string{
		"README.md",
		"kronos-server.service",
		"kronos-agent.service",
		"kronos-server.env.example",
		"kronos-agent.env.example",
	} {
		path := filepath.Join(root, name)
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("ReadFile(%s) error = %v", path, err)
		}
		if len(data) == 0 {
			t.Fatalf("%s is empty", path)
		}
	}

	serverUnit, err := os.ReadFile(filepath.Join(root, "kronos-server.service"))
	if err != nil {
		t.Fatalf("ReadFile(kronos-server.service) error = %v", err)
	}
	serverText := string(serverUnit)
	for _, want := range []string{
		"ExecStart=/usr/local/bin/kronos server --config /etc/kronos/kronos.yaml",
		"EnvironmentFile=-/etc/kronos/kronos-server.env",
		"StateDirectory=kronos",
		"NoNewPrivileges=true",
		"ProtectSystem=strict",
	} {
		if !strings.Contains(serverText, want) {
			t.Fatalf("kronos-server.service missing %q", want)
		}
	}

	agentUnit, err := os.ReadFile(filepath.Join(root, "kronos-agent.service"))
	if err != nil {
		t.Fatalf("ReadFile(kronos-agent.service) error = %v", err)
	}
	agentText := string(agentUnit)
	for _, want := range []string{
		"ExecStart=/usr/local/bin/kronos agent --work",
		"EnvironmentFile=-/etc/kronos/kronos-agent.env",
		"KRONOS_SERVER_URL",
		"NoNewPrivileges=true",
		"ProtectSystem=strict",
	} {
		if !strings.Contains(agentText, want) {
			t.Fatalf("kronos-agent.service missing %q", want)
		}
	}

	readme, err := os.ReadFile("operations.md")
	if err != nil {
		t.Fatalf("ReadFile(operations.md) error = %v", err)
	}
	if !strings.Contains(string(readme), "../contrib/systemd/README.md") {
		t.Fatalf("operations.md missing systemd runbook link")
	}
}

func TestGrafanaDashboardExampleExists(t *testing.T) {
	t.Parallel()

	root := filepath.Join("..", "contrib", "grafana")
	readme, err := os.ReadFile(filepath.Join(root, "README.md"))
	if err != nil {
		t.Fatalf("ReadFile(grafana README) error = %v", err)
	}
	for _, want := range []string{
		"kronos-overview.json",
		"Prometheus",
		"control-plane build and uptime",
		"backup freshness",
	} {
		if !strings.Contains(string(readme), want) {
			t.Fatalf("contrib/grafana/README.md missing %q", want)
		}
	}

	dashboardData, err := os.ReadFile(filepath.Join(root, "kronos-overview.json"))
	if err != nil {
		t.Fatalf("ReadFile(kronos-overview.json) error = %v", err)
	}
	var dashboard struct {
		Title      string `json:"title"`
		UID        string `json:"uid"`
		Templating struct {
			List []struct {
				Name string `json:"name"`
				Type string `json:"type"`
			} `json:"list"`
		} `json:"templating"`
		Panels []struct {
			Title   string `json:"title"`
			Targets []struct {
				Expr string `json:"expr"`
			} `json:"targets"`
		} `json:"panels"`
	}
	if err := json.Unmarshal(dashboardData, &dashboard); err != nil {
		t.Fatalf("invalid Grafana dashboard JSON: %v", err)
	}
	if dashboard.Title != "Kronos Overview" || dashboard.UID != "kronos-overview" {
		t.Fatalf("unexpected dashboard identity: title=%q uid=%q", dashboard.Title, dashboard.UID)
	}
	if len(dashboard.Templating.List) == 0 || dashboard.Templating.List[0].Name != "datasource" || dashboard.Templating.List[0].Type != "datasource" {
		t.Fatalf("dashboard missing datasource variable: %+v", dashboard.Templating.List)
	}
	dashboardText := string(dashboardData)
	for _, want := range []string{
		"kronos_build_info",
		"kronos_process_uptime_seconds",
		"kronos_agents_capacity",
		"kronos_jobs_active_by_operation",
		"kronos_backups_latest_completed_timestamp",
		"kronos_backups_bytes_by_target",
		"kronos_tokens_expired",
		"kronos_auth_rate_limited_total",
	} {
		if !strings.Contains(dashboardText, want) {
			t.Fatalf("dashboard missing %q", want)
		}
	}

	operations, err := os.ReadFile("operations.md")
	if err != nil {
		t.Fatalf("ReadFile(operations.md) error = %v", err)
	}
	if !strings.Contains(string(operations), "../contrib/grafana/kronos-overview.json") {
		t.Fatalf("operations.md missing Grafana dashboard link")
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
		"./scripts/verify-sbom.sh",
		"./scripts/smoke-release.sh",
		"govulncheck",
		"attestations: write",
		"id-token: write",
		"actions/attest-build-provenance@v4.1.0",
		"actions/attest@v4.1.0",
		"sigstore/cosign-installer@v4.1.1",
		"actions/upload-artifact@v7",
		"KRONOS_RELEASE_TAG_PUBLIC_KEY",
		"./scripts/archive-release-evidence.sh bin",
		"./scripts/verify-release-evidence.sh",
		"kronos-release-evidence-${{ github.ref_name }}",
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
		filepath.Join("..", "scripts", "verify-sbom.sh"),
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

func TestReleaseVerificationDocumentsSupplyChainChecks(t *testing.T) {
	t.Parallel()

	data, err := os.ReadFile("release-verification.md")
	if err != nil {
		t.Fatalf("ReadFile(release-verification.md) error = %v", err)
	}
	text := string(data)
	for _, want := range []string{
		"./scripts/verify-release.sh",
		"./scripts/verify-sbom.sh",
		"./scripts/verify-signatures.sh",
		"govulncheck",
		"./scripts/release-rehearsal.sh",
		"gh attestation verify",
		"--signer-workflow .github/workflows/release.yml",
		"Do not promote a release",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("release-verification.md missing %q", want)
		}
	}
}

func TestReleaseGateRequiresSBOMVulnerabilityVerification(t *testing.T) {
	t.Parallel()

	ci, err := os.ReadFile(filepath.Join("..", ".github", "workflows", "ci.yml"))
	if err != nil {
		t.Fatalf("ReadFile(ci.yml) error = %v", err)
	}
	release, err := os.ReadFile(filepath.Join("..", ".github", "workflows", "release.yml"))
	if err != nil {
		t.Fatalf("ReadFile(release.yml) error = %v", err)
	}
	verifySBOM, err := os.ReadFile(filepath.Join("..", "scripts", "verify-sbom.sh"))
	if err != nil {
		t.Fatalf("ReadFile(verify-sbom.sh) error = %v", err)
	}
	docs, err := os.ReadFile("release-verification.md")
	if err != nil {
		t.Fatalf("ReadFile(release-verification.md) error = %v", err)
	}

	for path, text := range map[string]string{
		".github/workflows/ci.yml":      string(ci),
		".github/workflows/release.yml": string(release),
	} {
		for _, want := range []string{
			"KRONOS_REQUIRE_GOVULNCHECK=1 ./scripts/verify-sbom.sh bin",
			"./scripts/verify-release.sh bin",
		} {
			if !strings.Contains(text, want) {
				t.Fatalf("%s missing %q", path, want)
			}
		}
	}
	for _, want := range []string{
		"require_govulncheck=\"${KRONOS_REQUIRE_GOVULNCHECK:-0}\"",
		"govulncheck is required but was not found",
		"\"$govulncheck_cmd\" ./...",
	} {
		if !strings.Contains(string(verifySBOM), want) {
			t.Fatalf("verify-sbom.sh missing %q", want)
		}
	}
	for _, want := range []string{
		"KRONOS_REQUIRE_GOVULNCHECK=1 ./scripts/verify-sbom.sh bin",
		"It is a source/module vulnerability gate, not a standalone",
		"Do not promote a release if any checksum, SBOM, vulnerability, signature, or",
	} {
		if !strings.Contains(string(docs), want) {
			t.Fatalf("release-verification.md missing %q", want)
		}
	}
}

func TestOperationsDocumentsUpgradeRollback(t *testing.T) {
	t.Parallel()

	data, err := os.ReadFile("operations.md")
	if err != nil {
		t.Fatalf("ReadFile(operations.md) error = %v", err)
	}
	text := string(data)
	for _, want := range []string{
		"### Upgrade Rollback",
		"/var/lib/kronos/rollback/kronos.previous",
		"/var/lib/kronos/rollback/state.db.previous",
		"sudo systemctl stop kronos-agent",
		"sudo systemctl stop kronos-server",
		"repair-db --db /var/lib/kronos/state.db",
		"kronos_build_info",
		"Treat rollback snapshots as sensitive data",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("operations.md missing %q", want)
		}
	}
}

func TestCIWorkflowCoversMongoDBConformanceVersions(t *testing.T) {
	t.Parallel()

	data, err := os.ReadFile(filepath.Join("..", ".github", "workflows", "ci.yml"))
	if err != nil {
		t.Fatalf("ReadFile(ci.yml) error = %v", err)
	}
	text := string(data)
	for _, want := range []string{
		"name: mongodb-conformance (${{ matrix.mongodb-version }})",
		"mongodb-version:",
		`- "7.0"`,
		`- "8.0"`,
		"mongodb/mongodb-community-server:${{ matrix.mongodb-version }}-ubuntu2204",
		"name: mongodb-oplog-rehearsal (7.0 replica set)",
		"KRONOS_MONGODB_OPLOG_TEST_ADDR",
		"KRONOS_MONGODB_OPLOG_RESTORE_ADDR",
		"TestMongoDBDriverReplicaSetOplogRehearsal",
		"go test -tags=integration ./internal/drivers/mongodb",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("ci.yml missing %q", want)
		}
	}
}

func TestCIWorkflowPassesPostgresPasswordToContainerizedClients(t *testing.T) {
	t.Parallel()

	data, err := os.ReadFile(filepath.Join("..", ".github", "workflows", "ci.yml"))
	if err != nil {
		t.Fatalf("ReadFile(ci.yml) error = %v", err)
	}
	const want = "--network host --env PGPASSWORD \"postgres:${POSTGRES_VERSION:?}\""
	if got := strings.Count(string(data), want); got != 4 {
		t.Fatalf("PostgreSQL client wrappers with PGPASSWORD pass-through = %d, want 4", got)
	}
}

func TestCIWorkflowCoversPostgresUpgradeRehearsalMatrix(t *testing.T) {
	t.Parallel()

	data, err := os.ReadFile(filepath.Join("..", ".github", "workflows", "ci.yml"))
	if err != nil {
		t.Fatalf("ReadFile(ci.yml) error = %v", err)
	}
	text := string(data)
	for _, want := range []string{
		"name: postgres-upgrade-rehearsal (${{ matrix.upgrade_path }})",
		"upgrade_path: 15-to-17",
		"upgrade_path: 16-to-17",
		"source_version: \"15\"",
		"source_version: \"16\"",
		"KRONOS_POSTGRES_TEST_DSN: postgres://postgres:postgres@127.0.0.1:${{ matrix.source_port }}/postgres?sslmode=disable",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("ci.yml missing %q", want)
		}
	}
}

func TestCIWorkflowMountsMongoTempConfigIntoContainerizedClients(t *testing.T) {
	t.Parallel()

	data, err := os.ReadFile(filepath.Join("..", ".github", "workflows", "ci.yml"))
	if err != nil {
		t.Fatalf("ReadFile(ci.yml) error = %v", err)
	}
	const want = `--network host --user "$(id -u):$(id -g)" --volume /tmp:/tmp "${MONGODB_IMAGE:?}"`
	if got := strings.Count(string(data), want); got != 3 {
		t.Fatalf("MongoDB client wrappers with temp config mount = %d, want 3", got)
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

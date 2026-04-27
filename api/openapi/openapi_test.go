package openapi_test

import (
	"os"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestOpenAPIReferencesResolve(t *testing.T) {
	t.Parallel()

	data, err := os.ReadFile("openapi.yaml")
	if err != nil {
		t.Fatalf("ReadFile(openapi.yaml) error = %v", err)
	}
	var doc map[string]any
	if err := yaml.Unmarshal(data, &doc); err != nil {
		t.Fatalf("Unmarshal(openapi.yaml) error = %v", err)
	}
	components, ok := doc["components"].(map[string]any)
	if !ok {
		t.Fatal("components missing")
	}
	sections := map[string]map[string]any{}
	for _, name := range []string{"headers", "parameters", "schemas", "securitySchemes"} {
		section, ok := components[name].(map[string]any)
		if !ok {
			t.Fatalf("components.%s missing", name)
		}
		sections[name] = section
	}
	for _, ref := range collectRefs(doc) {
		parts := strings.Split(strings.TrimPrefix(ref, "#/components/"), "/")
		if len(parts) != 2 {
			t.Fatalf("unsupported ref %q", ref)
		}
		section, ok := sections[parts[0]]
		if !ok {
			t.Fatalf("unknown component section in ref %q", ref)
		}
		if _, ok := section[parts[1]]; !ok {
			t.Fatalf("unresolved ref %q", ref)
		}
	}
}

func TestOpenAPIDocumentsRequestIDAndAuthRateLimit(t *testing.T) {
	t.Parallel()

	data, err := os.ReadFile("openapi.yaml")
	if err != nil {
		t.Fatalf("ReadFile(openapi.yaml) error = %v", err)
	}
	text := string(data)
	for _, want := range []string{"X-Kronos-Request-ID", "RequestID:", `"429":`, "Retry-After"} {
		if !strings.Contains(text, want) {
			t.Fatalf("openapi.yaml missing %q", want)
		}
	}
}

func TestOpenAPIDocumentsSecurityHeaders(t *testing.T) {
	t.Parallel()

	data, err := os.ReadFile("openapi.yaml")
	if err != nil {
		t.Fatalf("ReadFile(openapi.yaml) error = %v", err)
	}
	text := string(data)
	for _, want := range []string{"Cache-Control: no-store", "Content-Security-Policy", "X-Content-Type-Options", "X-Frame-Options", "NoStore:"} {
		if !strings.Contains(text, want) {
			t.Fatalf("openapi.yaml missing %q", want)
		}
	}
}

func TestOpenAPIDocumentsHeadProbes(t *testing.T) {
	t.Parallel()

	data, err := os.ReadFile("openapi.yaml")
	if err != nil {
		t.Fatalf("ReadFile(openapi.yaml) error = %v", err)
	}
	text := string(data)
	for _, want := range []string{"GET and HEAD", "Health check headers", "Readiness check headers", "Prometheus metrics headers", "Operations overview headers"} {
		if !strings.Contains(text, want) {
			t.Fatalf("openapi.yaml missing %q", want)
		}
	}
}

func TestOpenAPIDocumentsOverview(t *testing.T) {
	t.Parallel()

	data, err := os.ReadFile("openapi.yaml")
	if err != nil {
		t.Fatalf("ReadFile(openapi.yaml) error = %v", err)
	}
	text := string(data)
	for _, want := range []string{"/api/v1/overview", "OverviewResponse", "latest_jobs", "latest_backups", "notification_rules_enabled", "ReadinessResponse", "disabled_notification_rules"} {
		if !strings.Contains(text, want) {
			t.Fatalf("openapi.yaml missing %q", want)
		}
	}
}

func TestOpenAPIDocumentsNotificationRules(t *testing.T) {
	t.Parallel()

	data, err := os.ReadFile("openapi.yaml")
	if err != nil {
		t.Fatalf("ReadFile(openapi.yaml) error = %v", err)
	}
	text := string(data)
	for _, want := range []string{"/api/v1/notifications", "/api/v1/notifications/{id}", "NotificationRule", "job.failed", "webhook_url", "max_attempts"} {
		if !strings.Contains(text, want) {
			t.Fatalf("openapi.yaml missing %q", want)
		}
	}
}

func collectRefs(value any) []string {
	var refs []string
	switch typed := value.(type) {
	case map[string]any:
		for key, nested := range typed {
			if key == "$ref" {
				if ref, ok := nested.(string); ok && strings.HasPrefix(ref, "#/components/") {
					refs = append(refs, ref)
				}
				continue
			}
			refs = append(refs, collectRefs(nested)...)
		}
	case []any:
		for _, nested := range typed {
			refs = append(refs, collectRefs(nested)...)
		}
	}
	return refs
}

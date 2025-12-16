package api

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func findRepoRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	dir := wd
	for i := 0; i < 20; i++ {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		next := filepath.Dir(dir)
		if next == dir {
			break
		}
		dir = next
	}
	t.Fatalf("repo root not found from %s", wd)
	return ""
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}

func TestDocsContractV1RoutesAreDocumented(t *testing.T) {
	root := findRepoRoot(t)
	spec := readFile(t, filepath.Join(root, "docs", "api", "API_AND_EVENT_SPEC.md"))

	needles := []string{
		"GET /api/v1/health",
		"GET /api/v1/pools",
		"GET /api/v1/pools/{pool_id}/state",
		"GET /api/v1/intents",
		"GET /api/v1/intents/{intent_id}",
		"GET /api/v1/tx",
		"GET /api/v1/audit",
		"GET /api/v1/stream",
		"POST /api/v1/operations/preview",
		"POST /api/v1/operations/execute",
		"POST /api/v1/pools/{pool_id}/pause",
		"POST /api/v1/pools/{pool_id}/resume",
	}

	for _, n := range needles {
		if !strings.Contains(spec, n) {
			t.Fatalf("docs/api/API_AND_EVENT_SPEC.md missing %q", n)
		}
	}
	if !strings.Contains(spec, `"error": { "code"`) {
		t.Fatalf("docs/api/API_AND_EVENT_SPEC.md missing unified error shape")
	}

	serverGo := readFile(t, filepath.Join(root, "internal", "api", "server.go"))
	for _, p := range []string{
		`mux.HandleFunc("/api/v1/health"`,
		`mux.HandleFunc("/api/v1/pools"`,
		`mux.HandleFunc("/api/v1/pools/"`,
		`mux.HandleFunc("/api/v1/intents"`,
		`mux.HandleFunc("/api/v1/intents/"`,
		`mux.HandleFunc("/api/v1/tx"`,
		`mux.HandleFunc("/api/v1/audit"`,
		`mux.HandleFunc("/api/v1/operations/preview"`,
		`mux.HandleFunc("/api/v1/operations/execute"`,
		`mux.HandleFunc("/api/v1/stream"`,
	} {
		if !strings.Contains(serverGo, p) {
			t.Fatalf("internal/api/server.go missing v1 route: %s", p)
		}
	}

	serverV1Go := readFile(t, filepath.Join(root, "internal", "api", "server_v1.go"))
	for _, seg := range []string{"case \"pause\":", "case \"resume\":"} {
		if !strings.Contains(serverV1Go, seg) {
			t.Fatalf("internal/api/server_v1.go missing pool subroute: %s", seg)
		}
	}
}

func TestDocsContractNoUndocumentedV1RoutesInServer(t *testing.T) {
	root := findRepoRoot(t)
	spec := readFile(t, filepath.Join(root, "docs", "api", "API_AND_EVENT_SPEC.md"))
	serverGo := readFile(t, filepath.Join(root, "internal", "api", "server.go"))

	// Extract all v1 mux registrations from server.go and ensure each is mentioned in the docs.
	re := regexp.MustCompile(`mux\.HandleFunc\("(/api/v1/[^"[:space:]]+)"`)
	matches := re.FindAllStringSubmatch(serverGo, -1)
	if len(matches) == 0 {
		t.Fatalf("no /api/v1 routes found in internal/api/server.go")
	}
	for _, m := range matches {
		route := m[1]
		if strings.HasSuffix(route, "/pools/") || strings.HasSuffix(route, "/intents/") {
			continue
		}
		if !strings.Contains(spec, route) {
			t.Fatalf("v1 route %q is not documented in docs/api/API_AND_EVENT_SPEC.md", route)
		}
	}
}

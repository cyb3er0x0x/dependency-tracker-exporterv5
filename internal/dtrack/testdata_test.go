package dtrack

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// serveFiles maps request paths to fixture files under testdata/.
func serveFiles(t *testing.T, routes map[string]string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	for path, file := range routes {
		body, err := os.ReadFile(filepath.Join("testdata", file))
		if err != nil {
			t.Fatalf("read fixture %s: %v", file, err)
		}
		mux.HandleFunc(path, func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("X-Total-Count", "1")
			_, _ = w.Write(body)
		})
	}
	return httptest.NewServer(mux)
}

func TestDecode_V1Fixtures(t *testing.T) {
	srv := serveFiles(t, map[string]string{
		"/api/v1/project":   "v1/project_page.json",
		"/api/v1/violation": "v1/violation_page.json",
	})
	defer srv.Close()
	c := newTestClient(t, srv.URL)

	projects, err := c.Projects(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(projects) != 1 || projects[0].Name != "acme-api" || projects[0].Metrics.Critical != 2 {
		t.Fatalf("unexpected project: %+v", projects)
	}
	if l, known := projects[0].LatestState(); !known || !l {
		t.Fatalf("expected latest state true/known, got %v/%v", l, known)
	}

	vs, err := c.PolicyViolations(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(vs) != 1 || vs[0].State() != "WARN" || vs[0].Type != "LICENSE" {
		t.Fatalf("unexpected violation: %+v", vs)
	}
	if st, sup := vs[0].AnalysisState(); st != "APPROVED" || sup {
		t.Fatalf("unexpected analysis: %s/%v", st, sup)
	}
}

func TestDecode_V2Fixtures(t *testing.T) {
	srv := serveFiles(t, map[string]string{
		"/api/version":              "v1/version.json",
		"/api/v2/projects":          "v2/projects_page.json",
		"/api/v2/policy-violations": "v2/violations_empty.json",
	})
	defer srv.Close()

	c := newTestClient(t, srv.URL, func(o *Options) { o.APIVersion = APIAuto })
	c.Probe(context.Background())
	if !c.v2Projects {
		t.Fatal("expected v2 projects enabled")
	}
	projects, err := c.Projects(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(projects) != 1 || projects[0].Metrics.High != 5 {
		t.Fatalf("unexpected v2 projects: %+v", projects)
	}
}

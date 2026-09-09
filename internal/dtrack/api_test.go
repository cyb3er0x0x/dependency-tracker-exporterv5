package dtrack

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func newTestClient(t *testing.T, url string, opts ...func(*Options)) *Client {
	t.Helper()
	o := Options{BaseURL: url, APIKey: "secret-key", APIVersion: APIv1, PageSize: 100, RequestTimeout: 2 * time.Second}
	for _, fn := range opts {
		fn(&o)
	}
	c, err := New(o)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c
}

func TestProjects_OffsetPagination(t *testing.T) {
	const total = 468
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/project", func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("X-Api-Key"); got != "secret-key" {
			t.Errorf("missing/incorrect API key header: %q", got)
		}
		size, _ := strconv.Atoi(r.URL.Query().Get("pageSize"))
		page, _ := strconv.Atoi(r.URL.Query().Get("pageNumber"))
		if size != 100 {
			t.Errorf("pageSize = %d, want 100", size)
		}
		w.Header().Set("X-Total-Count", strconv.Itoa(total))
		var out []Project
		for i := 0; i < size; i++ {
			idx := (page-1)*size + i
			if idx >= total {
				break
			}
			out = append(out, Project{UUID: fmt.Sprintf("uuid-%d", idx)})
		}
		_ = json.NewEncoder(w).Encode(out)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	got, err := newTestClient(t, srv.URL).Projects(context.Background())
	if err != nil {
		t.Fatalf("Projects: %v", err)
	}
	if len(got) != total {
		t.Fatalf("got %d projects, want %d", len(got), total)
	}
	if got[0].UUID != "uuid-0" || got[total-1].UUID != "uuid-467" {
		t.Fatalf("unexpected first/last: %s / %s", got[0].UUID, got[total-1].UUID)
	}
}

func TestProjects_TokenPagination_V2(t *testing.T) {
	pages := [][]Project{
		{{UUID: "a"}, {UUID: "b"}},
		{{UUID: "c"}, {UUID: "d"}},
		{{UUID: "e"}},
	}
	tokens := []string{"t1", "t2", ""}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/version", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"version": "5.1.0"})
	})
	mux.HandleFunc("/api/v2/projects", func(w http.ResponseWriter, r *http.Request) {
		idx := 0
		switch r.URL.Query().Get("pageToken") {
		case "t1":
			idx = 1
		case "t2":
			idx = 2
		}
		_ = json.NewEncoder(w).Encode(tokenPage[Project]{Items: pages[idx], NextPageToken: tokens[idx]})
	})
	mux.HandleFunc("/api/v2/policy-violations", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(tokenPage[PolicyViolation]{})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := newTestClient(t, srv.URL, func(o *Options) { o.APIVersion = APIAuto })
	c.Probe(context.Background())
	if !c.v2Projects {
		t.Fatal("expected v2Projects to be enabled after probe")
	}
	if c.ServerVersion() != "5.1.0" {
		t.Fatalf("server version = %q", c.ServerVersion())
	}
	got, err := c.Projects(context.Background())
	if err != nil {
		t.Fatalf("Projects: %v", err)
	}
	if len(got) != 5 || got[0].UUID != "a" || got[4].UUID != "e" {
		t.Fatalf("unexpected projects: %+v", got)
	}
}

func TestDo_RetriesThenSucceeds(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if atomic.AddInt32(&calls, 1) < 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		_ = json.NewEncoder(w).Encode(Metrics{Critical: 7})
	}))
	defer srv.Close()

	c := newTestClient(t, srv.URL, func(o *Options) { o.RetryMax = 3; o.RetryBaseDelay = time.Millisecond })
	m, err := c.PortfolioMetrics(context.Background())
	if err != nil {
		t.Fatalf("PortfolioMetrics: %v", err)
	}
	if m.Critical != 7 {
		t.Fatalf("Critical = %d", m.Critical)
	}
	if calls != 3 {
		t.Fatalf("calls = %d, want 3", calls)
	}
}

func TestDo_RetriesExhausted(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer srv.Close()
	c := newTestClient(t, srv.URL, func(o *Options) { o.RetryMax = 2; o.RetryBaseDelay = time.Millisecond })
	if _, err := c.PortfolioMetrics(context.Background()); err == nil {
		t.Fatal("expected error after exhausting retries")
	}
}

func TestDo_Timeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(200 * time.Millisecond)
		_ = json.NewEncoder(w).Encode(Metrics{})
	}))
	defer srv.Close()
	c := newTestClient(t, srv.URL, func(o *Options) { o.RequestTimeout = 20 * time.Millisecond; o.RetryMax = 0 })
	if _, err := c.PortfolioMetrics(context.Background()); err == nil {
		t.Fatal("expected timeout error")
	}
}

func TestSecret_NeverLeaks(t *testing.T) {
	s := Secret("super-secret-value")
	for _, got := range []string{
		s.String(),
		fmt.Sprintf("%s", s),
		fmt.Sprintf("%v", s),
		fmt.Sprintf("%q", s),
		fmt.Sprintf("%#v", s),
	} {
		if strings.Contains(got, "super-secret-value") {
			t.Fatalf("secret leaked in %q", got)
		}
	}
	b, _ := json.Marshal(struct{ Key Secret }{s})
	if strings.Contains(string(b), "super-secret-value") {
		t.Fatalf("secret leaked in JSON: %s", b)
	}
	if s.Reveal() != "super-secret-value" {
		t.Fatal("Reveal should return the real value")
	}
}

func TestNew_ClampsPageSize(t *testing.T) {
	for _, tc := range []struct{ in, want int }{{0, 100}, {-5, 1}, {9000, 500}, {250, 250}} {
		c, err := New(Options{BaseURL: "http://x", APIKey: "k", PageSize: tc.in})
		if err != nil {
			t.Fatal(err)
		}
		if c.PageSize() != tc.want {
			t.Errorf("pageSize(%d) = %d, want %d", tc.in, c.PageSize(), tc.want)
		}
	}
}

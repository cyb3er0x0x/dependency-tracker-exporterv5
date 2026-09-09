package collector

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/cyb3er0x0x/dependency-tracker-exporterv5/internal/dtrack"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

type fakeAPI struct {
	portfolio  dtrack.Metrics
	projects   []dtrack.Project
	violations []dtrack.PolicyViolation
	err        error
	calls      int
}

func (f *fakeAPI) PortfolioMetrics(context.Context) (dtrack.Metrics, error) {
	f.calls++
	if f.err != nil {
		return dtrack.Metrics{}, f.err
	}
	return f.portfolio, nil
}
func (f *fakeAPI) Projects(context.Context) ([]dtrack.Project, error) { return f.projects, f.err }
func (f *fakeAPI) PolicyViolations(context.Context) ([]dtrack.PolicyViolation, error) {
	return f.violations, f.err
}
func (f *fakeAPI) ServerVersion() string { return "5.1.0" }

func TestCollector_HappyPathAndFailureKeepsSnapshot(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	api := &fakeAPI{
		portfolio: dtrack.Metrics{Critical: 3},
		projects:  []dtrack.Project{{UUID: "a", Name: "app"}},
	}
	store := NewStore()
	c := New(Options{
		Client: api, Store: store,
		RefreshInterval: time.Hour, CollectionTimeout: time.Minute,
		Now: func() time.Time { return now },
	})

	if err := c.CollectNow(context.Background()); err != nil {
		t.Fatalf("first collect: %v", err)
	}
	snap := store.Get()
	if snap == nil || snap.Portfolio.Critical != 3 || len(snap.Projects) != 1 {
		t.Fatalf("unexpected snapshot: %+v", snap)
	}

	// Second collection fails: snapshot must be retained, errors_total == 1.
	api.err = errors.New("boom")
	if err := c.CollectNow(context.Background()); err == nil {
		t.Fatal("expected error")
	}
	if store.Get() != snap {
		t.Fatal("failed refresh replaced the good snapshot")
	}
	if got := testutil.ToFloat64(c.errorsTotal); got != 1 {
		t.Fatalf("collection_errors_total = %v, want 1", got)
	}
	if got := testutil.ToFloat64(c.lastSuccessSec); got != float64(now.Unix()) {
		t.Fatalf("last_success_timestamp_seconds = %v", got)
	}
}

func TestCollector_CacheAgeSeconds(t *testing.T) {
	cur := time.Unix(2_000_000, 0)
	api := &fakeAPI{}
	store := NewStore()
	c := New(Options{Client: api, Store: store, Now: func() time.Time { return cur }})
	if err := c.CollectNow(context.Background()); err != nil {
		t.Fatal(err)
	}
	cur = cur.Add(90 * time.Second)

	if err := testutil.CollectAndCompare(c, strings.NewReader(`
# HELP dependency_track_exporter_cache_age_seconds Age of the currently served portfolio snapshot, in seconds.
# TYPE dependency_track_exporter_cache_age_seconds gauge
dependency_track_exporter_cache_age_seconds 90
`), "dependency_track_exporter_cache_age_seconds"); err != nil {
		t.Fatal(err)
	}
}

var _ prometheus.Collector = (*Collector)(nil)

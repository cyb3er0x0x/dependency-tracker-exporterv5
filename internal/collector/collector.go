package collector

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/cyb3er0x0x/dependency-tracker-exporterv5/internal/dtrack"
	"github.com/go-kit/log"
	"github.com/go-kit/log/level"
	"github.com/prometheus/client_golang/prometheus"
)

// SelfNamespace is the metric namespace for the exporter's own metrics.
const SelfNamespace = "dependency_track_exporter"

// API is the subset of the Dependency-Track client the collector needs. It is an
// interface so tests can supply a fake.
type API interface {
	PortfolioMetrics(ctx context.Context) (dtrack.Metrics, error)
	Projects(ctx context.Context) ([]dtrack.Project, error)
	PolicyViolations(ctx context.Context) ([]dtrack.PolicyViolation, error)
	ServerVersion() string
}

// Options configures a Collector.
type Options struct {
	Client            API
	Store             *Store
	Logger            log.Logger
	RefreshInterval   time.Duration
	CollectionTimeout time.Duration
	// Now is injectable for tests; defaults to time.Now.
	Now func() time.Time
}

// Collector periodically refreshes the portfolio Snapshot in the background and
// exposes the exporter's self-metrics.
type Collector struct {
	opts Options
	now  func() time.Time

	mu             sync.Mutex // serialises collectOnce
	durationSec    prometheus.Gauge
	errorsTotal    prometheus.Counter
	lastSuccessSec prometheus.Gauge
	cacheAgeDesc   *prometheus.Desc
}

// New builds a Collector and registers its self-metrics on reg.
func New(o Options) *Collector {
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.RefreshInterval <= 0 {
		o.RefreshInterval = 5 * time.Minute
	}
	if o.CollectionTimeout <= 0 {
		o.CollectionTimeout = 4 * time.Minute
	}
	if o.Logger == nil {
		o.Logger = log.NewNopLogger()
	}
	c := &Collector{
		opts: o,
		now:  o.Now,
		durationSec: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: SelfNamespace,
			Name:      "collection_duration_seconds",
			Help:      "Duration of the most recent portfolio collection from Dependency-Track.",
		}),
		errorsTotal: prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: SelfNamespace,
			Name:      "collection_errors_total",
			Help:      "Total number of failed portfolio collections.",
		}),
		lastSuccessSec: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: SelfNamespace,
			Name:      "last_success_timestamp_seconds",
			Help:      "Unix timestamp of the last successful portfolio collection.",
		}),
		cacheAgeDesc: prometheus.NewDesc(
			SelfNamespace+"_cache_age_seconds",
			"Age of the currently served portfolio snapshot, in seconds.",
			nil, nil,
		),
	}
	return c
}

// Describe implements prometheus.Collector.
func (c *Collector) Describe(ch chan<- *prometheus.Desc) {
	c.durationSec.Describe(ch)
	c.errorsTotal.Describe(ch)
	c.lastSuccessSec.Describe(ch)
	ch <- c.cacheAgeDesc
}

// Collect implements prometheus.Collector. cache_age_seconds is computed at
// scrape time from the current snapshot.
func (c *Collector) Collect(ch chan<- prometheus.Metric) {
	c.durationSec.Collect(ch)
	c.errorsTotal.Collect(ch)
	c.lastSuccessSec.Collect(ch)

	var age float64
	if snap := c.opts.Store.Get(); snap != nil {
		age = c.now().Sub(snap.CollectedAt).Seconds()
	}
	ch <- prometheus.MustNewConstMetric(c.cacheAgeDesc, prometheus.GaugeValue, age)
}

// Run performs an initial synchronous collection, then refreshes on the
// configured interval until ctx is cancelled.
func (c *Collector) Run(ctx context.Context) {
	if err := c.collectOnce(ctx); err != nil {
		level.Warn(c.opts.Logger).Log("msg", "initial collection failed; /metrics will be sparse until it succeeds", "err", err)
	}

	t := time.NewTicker(c.opts.RefreshInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := c.collectOnce(ctx); err != nil {
				level.Error(c.opts.Logger).Log("msg", "portfolio collection failed; keeping previous snapshot", "err", err)
			}
		}
	}
}

// CollectNow triggers an out-of-band refresh (used by the initial call and
// optionally an admin endpoint).
func (c *Collector) CollectNow(ctx context.Context) error { return c.collectOnce(ctx) }

func (c *Collector) collectOnce(parent context.Context) (err error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// A panic while decoding or rendering untrusted Dependency-Track responses
	// must not kill the background refresh goroutine.
	defer func() {
		if r := recover(); r != nil {
			c.errorsTotal.Inc()
			err = fmt.Errorf("panic during collection: %v", r)
		}
	}()

	ctx, cancel := context.WithTimeout(parent, c.opts.CollectionTimeout)
	defer cancel()

	start := c.now()
	portfolio, err := c.opts.Client.PortfolioMetrics(ctx)
	if err != nil {
		c.errorsTotal.Inc()
		return err
	}
	projects, err := c.opts.Client.Projects(ctx)
	if err != nil {
		c.errorsTotal.Inc()
		return err
	}
	violations, err := c.opts.Client.PolicyViolations(ctx)
	if err != nil {
		c.errorsTotal.Inc()
		return err
	}

	done := c.now()
	dur := done.Sub(start)
	c.opts.Store.Set(&Snapshot{
		CollectedAt:   done,
		Duration:      dur,
		Portfolio:     portfolio,
		Projects:      projects,
		Violations:    violations,
		ServerVersion: c.opts.Client.ServerVersion(),
	})
	c.durationSec.Set(dur.Seconds())
	c.lastSuccessSec.Set(float64(done.Unix()))
	level.Info(c.opts.Logger).Log("msg", "portfolio snapshot refreshed",
		"projects", len(projects), "violations", len(violations),
		"duration_seconds", dur.Seconds())
	return nil
}

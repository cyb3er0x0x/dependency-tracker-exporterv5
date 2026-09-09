// Package config holds the exporter's runtime configuration and its flag/env
// wiring.
package config

import (
	"fmt"
	"time"

	"github.com/alecthomas/kingpin/v2"
	"github.com/cyb3er0x0x/dependency-tracker-exporterv5/internal/dtrack"
)

const (
	envAddr       = "DEPENDENCY_TRACK_ADDR"
	envAPIKey     = "DEPENDENCY_TRACK_API_KEY"
	envAPIVersion = "DEPENDENCY_TRACK_API_VERSION"
	envRefresh    = "DEPENDENCY_TRACK_REFRESH_INTERVAL"
	envTimeout    = "DEPENDENCY_TRACK_TIMEOUT"
)

// Config is the fully-resolved exporter configuration.
type Config struct {
	Address    string
	APIKey     dtrack.Secret
	APIVersion dtrack.APIVersion

	RefreshInterval   time.Duration
	CollectionTimeout time.Duration
	RequestTimeout    time.Duration
	RetryMax          int
	RetryBaseDelay    time.Duration
	PageSize          int
	MaxConcurrency    int
	TLSInsecure       bool

	MetricsPath string
}

// Bind registers all exporter flags on app and returns a function that
// materialises the Config after parsing.
func Bind(app *kingpin.Application) func() (*Config, error) {
	var (
		addr       = app.Flag("dtrack.address", fmt.Sprintf("Dependency-Track server address (env $%s).", envAddr)).Default("http://localhost:8080").Envar(envAddr).String()
		apiKey     = app.Flag("dtrack.api-key", fmt.Sprintf("Dependency-Track API key (env $%s). Never logged.", envAPIKey)).Envar(envAPIKey).Required().String()
		apiVersion = app.Flag("dtrack.api-version", "Which Dependency-Track REST API to use: v1, v2 or auto.").Default("auto").Envar(envAPIVersion).Enum("v1", "v2", "auto")
		refresh    = app.Flag("dtrack.refresh-interval", fmt.Sprintf("How often to refresh the portfolio snapshot in the background (env $%s).", envRefresh)).Default("5m").Envar(envRefresh).Duration()
		collTO     = app.Flag("dtrack.collection-timeout", "Overall deadline for a single background collection.").Default("4m").Duration()
		reqTO      = app.Flag("dtrack.timeout", fmt.Sprintf("Per-request timeout for Dependency-Track API calls (env $%s).", envTimeout)).Default("30s").Envar(envTimeout).Duration()
		retryMax   = app.Flag("dtrack.retry-max", "Maximum retries for failed Dependency-Track requests.").Default("3").Int()
		retryBase  = app.Flag("dtrack.retry-base-delay", "Base delay for exponential backoff between retries.").Default("500ms").Duration()
		pageSize   = app.Flag("dtrack.page-size", "API page size for paginated requests (clamped to [1,500]).").Default("100").Int()
		maxConc    = app.Flag("dtrack.max-concurrency", "Maximum concurrent per-project API calls.").Default("4").Int()
		tlsInsec   = app.Flag("dtrack.tls-insecure-skip-verify", "Disable TLS certificate verification for Dependency-Track.").Default("false").Bool()
		metricsP   = app.Flag("web.metrics-path", "Path under which to expose metrics.").Default("/metrics").String()
	)

	return func() (*Config, error) {
		c := &Config{
			Address:           *addr,
			APIKey:            dtrack.Secret(*apiKey),
			APIVersion:        dtrack.APIVersion(*apiVersion),
			RefreshInterval:   *refresh,
			CollectionTimeout: *collTO,
			RequestTimeout:    *reqTO,
			RetryMax:          *retryMax,
			RetryBaseDelay:    *retryBase,
			PageSize:          *pageSize,
			MaxConcurrency:    *maxConc,
			TLSInsecure:       *tlsInsec,
			MetricsPath:       *metricsP,
		}
		if c.RefreshInterval < time.Second {
			return nil, fmt.Errorf("dtrack.refresh-interval must be >= 1s")
		}
		if c.CollectionTimeout <= 0 {
			return nil, fmt.Errorf("dtrack.collection-timeout must be > 0")
		}
		return c, nil
	}
}

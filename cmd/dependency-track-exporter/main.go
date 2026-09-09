package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/alecthomas/kingpin/v2"
	"github.com/cyb3er0x0x/dependency-tracker-exporterv5/internal/collector"
	"github.com/cyb3er0x0x/dependency-tracker-exporterv5/internal/config"
	"github.com/cyb3er0x0x/dependency-tracker-exporterv5/internal/dtrack"
	"github.com/cyb3er0x0x/dependency-tracker-exporterv5/internal/exporter"
	"github.com/go-kit/log/level"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/prometheus/common/promlog"
	promlogflag "github.com/prometheus/common/promlog/flag"
	"github.com/prometheus/common/version"
	"github.com/prometheus/exporter-toolkit/web"
	webflag "github.com/prometheus/exporter-toolkit/web/kingpinflag"
)

func main() {
	app := kingpin.New("dependency-track-exporter", "Prometheus exporter for OWASP Dependency-Track 4.12+/5.x.")
	app.HelpFlag.Short('h')

	webConfig := webflag.AddFlags(app, ":9916")
	newConfig := config.Bind(app)
	promlogConfig := promlog.Config{}
	promlogflag.AddFlags(app, &promlogConfig)
	app.Version(version.Print("dependency-track-exporter"))

	kingpin.MustParse(app.Parse(os.Args[1:]))
	logger := promlog.New(&promlogConfig)

	cfg, err := newConfig()
	if err != nil {
		level.Error(logger).Log("msg", "invalid configuration", "err", err)
		os.Exit(1)
	}

	level.Info(logger).Log("msg", "starting dependency-track-exporter", "version", version.Info(),
		"address", cfg.Address, "api_version", cfg.APIVersion, "refresh_interval", cfg.RefreshInterval)

	client, err := dtrack.New(dtrack.Options{
		BaseURL:            cfg.Address,
		APIKey:             cfg.APIKey,
		APIVersion:         cfg.APIVersion,
		UserAgent:          "dependency-track-exporter/" + version.Version,
		RequestTimeout:     cfg.RequestTimeout,
		RetryMax:           cfg.RetryMax,
		RetryBaseDelay:     cfg.RetryBaseDelay,
		PageSize:           cfg.PageSize,
		InsecureSkipVerify: cfg.TLSInsecure,
	})
	if err != nil {
		level.Error(logger).Log("msg", "error creating Dependency-Track client", "err", err)
		os.Exit(1)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	probeCtx, probeCancel := context.WithTimeout(ctx, cfg.RequestTimeout)
	client.Probe(probeCtx)
	probeCancel()
	level.Info(logger).Log("msg", "probed Dependency-Track", "server_version", client.ServerVersion(), "page_size", client.PageSize())

	store := collector.NewStore()
	coll := collector.New(collector.Options{
		Client:            client,
		Store:             store,
		Logger:            logger,
		RefreshInterval:   cfg.RefreshInterval,
		CollectionTimeout: cfg.CollectionTimeout,
	})

	reg := prometheus.NewRegistry()
	reg.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
		version.NewCollector("dependency_track_exporter"),
		coll,
		exporter.New(store),
	)

	go coll.Run(ctx)

	mux := http.NewServeMux()
	mux.Handle(cfg.MetricsPath, promhttp.HandlerFor(reg, promhttp.HandlerOpts{Registry: reg}))
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, _ *http.Request) {
		if store.Get() == nil {
			http.Error(w, "no snapshot yet", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ready"))
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprintf(w, `<html>
<head><title>Dependency-Track Exporter</title></head>
<body>
<h1>Dependency-Track Exporter</h1>
<p><a href=%q>Metrics</a></p>
</body>
</html>`, cfg.MetricsPath)
	})

	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}

	term := make(chan os.Signal, 1)
	signal.Notify(term, os.Interrupt, syscall.SIGTERM)
	errc := make(chan error, 1)
	go func() {
		if err := web.ListenAndServe(srv, webConfig, logger); err != nil && err != http.ErrServerClosed {
			errc <- err
		}
	}()

	select {
	case <-term:
		level.Info(logger).Log("msg", "received signal, shutting down")
		cancel()
		shutdownCtx, sc := context.WithTimeout(context.Background(), 5*time.Second)
		defer sc()
		_ = srv.Shutdown(shutdownCtx)
	case err := <-errc:
		level.Error(logger).Log("msg", "http server error", "err", err)
		os.Exit(1)
	}
}

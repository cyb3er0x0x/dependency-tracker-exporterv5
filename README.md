# dependency-track-exporter

Prometheus exporter for [OWASP Dependency-Track](https://dependencytrack.org/)
**4.12+ and 5.x**.

This is a modernized fork of
[`jetstack/dependency-track-exporter`](https://github.com/jetstack/dependency-track-exporter).
The key differences:

| | upstream | this fork |
|---|---|---|
| DT API calls | on **every** `/metrics` scrape | in a **background loop** (default every 5m) |
| `/metrics` latency | grows with portfolio size | constant — serves an in-memory snapshot |
| Failed DT query | scrape returns HTTP 500 | previous snapshot is kept, scrape still 200 |
| API page size | hard-coded 50 | configurable, default 100 (`[1,500]`) |
| REST API | v1 only | v1, plus **v2 + `next_page_token`** where available (`--dtrack.api-version`) |
| Retries / timeouts | none / library default | configurable (`--dtrack.retry-max`, `--dtrack.timeout`) |
| Self-observability | none | collection duration, errors, last-success, cache age |

Existing Prometheus metric **names and labels are preserved**, so dashboards and
alerts keep working (the `dependency_track_project_info` metric gains one extra
`latest` label, which is backwards compatible).

## Why

On large portfolios the upstream exporter issues
`ceil(projects/50) + ceil(violations/50)` sequential Dependency-Track API
requests *per scrape*. Dependency-Track 5.1 also enforces a global database
query timeout, so `/api/v1/violation` pagination increasingly fails outright.
Decoupling collection from scraping fixes both: Prometheus always gets a fast
answer, and Dependency-Track is queried at a predictable, low rate.

## Install

### Container image

```
docker run --rm -p 9916:9916 \
  -e DEPENDENCY_TRACK_ADDR=https://dtrack.example.com \
  -e DEPENDENCY_TRACK_API_KEY=odt_xxx \
  ghcr.io/cyb3er0x0x/dependency-tracker-exporterv5:v0.1.0
```

Pin a released tag (`:v0.1.0`) in production; `:latest` tracks the newest release.
Images are multi-arch (`linux/amd64`, `linux/arm64`).

### Pre-built binary

Download from the [releases page](https://github.com/cyb3er0x0x/dependency-tracker-exporterv5/releases):

```
VERSION=0.1.0
curl -sSL -o dte.tar.gz \
  https://github.com/cyb3er0x0x/dependency-tracker-exporterv5/releases/download/v${VERSION}/dependency-tracker-exporterv5_${VERSION}_linux_amd64.tar.gz
tar -xzf dte.tar.gz dependency-track-exporter
./dependency-track-exporter --help
```

### From source

```
go install github.com/cyb3er0x0x/dependency-tracker-exporterv5/cmd/dependency-track-exporter@v0.1.0
```

## Running

```
dependency-track-exporter \
  --dtrack.address=https://dtrack.example.com \
  --dtrack.api-key=odt_xxx
```

The API key needs the permissions `VIEW_PORTFOLIO`, `VIEW_POLICY_VIOLATION` and
`VIEW_VULNERABILITY`. The key is **never written to logs** (it is redacted to
`***` in every log line and error message).

Rootless Podman / Quadlet: see [`packaging/dependency-track-exporter.container`](packaging/dependency-track-exporter.container).
The image runs as UID `65532` with a read-only root filesystem.

### Flags

```
--dtrack.address              DT server address           ($DEPENDENCY_TRACK_ADDR, default http://localhost:8080)
--dtrack.api-key              DT API key (required)        ($DEPENDENCY_TRACK_API_KEY)
--dtrack.api-version          v1 | v2 | auto               ($DEPENDENCY_TRACK_API_VERSION, default auto)
--dtrack.refresh-interval     background refresh interval  ($DEPENDENCY_TRACK_REFRESH_INTERVAL, default 5m)
--dtrack.timeout              per-request timeout          ($DEPENDENCY_TRACK_TIMEOUT, default 30s)
--dtrack.collection-timeout   whole-collection deadline    (default 4m)
--dtrack.retry-max            retries on 5xx / timeout     (default 3)
--dtrack.retry-base-delay     backoff base delay           (default 500ms)
--dtrack.page-size            API page size, clamped [1,500] (default 100)
--dtrack.max-concurrency      max concurrent per-project calls (default 4)
--dtrack.tls-insecure-skip-verify   disable TLS verification (default false)
--web.listen-address          (default :9916)
--web.metrics-path            (default /metrics)
--web.config.file             exporter-toolkit TLS/basic-auth config
--log.level / --log.format
```

`auto` probes `GET /api/version` and the v2 collections at startup and uses v2
for any resource that answers; otherwise it falls back to v1.

### Endpoints

| Path | Purpose |
|---|---|
| `/metrics` | cached snapshot, always fast |
| `/healthz` | liveness — always 200 |
| `/readyz` | readiness — 200 once the first snapshot exists |

## Metrics

### Preserved (backwards compatible)

```
dependency_track_portfolio_inherited_risk_score
dependency_track_portfolio_vulnerabilities{severity}
dependency_track_portfolio_findings{audited}
dependency_track_project_info{uuid,name,version,classifier,active,latest,tags}
dependency_track_project_vulnerabilities{uuid,name,version,severity}
dependency_track_project_policy_violations{uuid,name,version,type,state,analysis,suppressed}
dependency_track_project_last_bom_import{uuid,name,version}
dependency_track_project_inherited_risk_score{uuid,name,version}
```

### New portfolio metrics

```
dependency_track_portfolio_suppressed
dependency_track_portfolio_projects
dependency_track_portfolio_components
dependency_track_portfolio_vulnerable_projects
dependency_track_portfolio_vulnerable_components
dependency_track_portfolio_findings_total
dependency_track_portfolio_kev
dependency_track_portfolio_policy_violations{state="FAIL|WARN|INFO"}
dependency_track_portfolio_policy_violations_by_class{class,audited}
```

### New project metrics

```
dependency_track_project_findings{uuid,name,version,audited}
dependency_track_project_findings_total{uuid,name,version}
dependency_track_project_suppressed{uuid,name,version}
dependency_track_project_components{uuid,name,version}
dependency_track_project_kev{uuid,name,version}
dependency_track_project_policy_violations_total{uuid,name,version,type,state}
```

`*_kev` is only emitted when the running Dependency-Track version reports KEV
counts in its metrics payload.

### Exporter self-metrics

```
dependency_track_exporter_collection_duration_seconds
dependency_track_exporter_collection_errors_total
dependency_track_exporter_last_success_timestamp_seconds
dependency_track_exporter_cache_age_seconds
dependency_track_exporter_build_info
```

## Prometheus scrape config

Because scrapes no longer touch Dependency-Track, a normal interval and a small
timeout are safe:

```yaml
scrape_configs:
  - job_name: dependency-track
    scrape_interval: 60s
    scrape_timeout: 15s
    static_configs:
      - targets: ["dependency-track-exporter:9916"]
```

Tune `--dtrack.refresh-interval` (not the scrape interval) to control load on
Dependency-Track.

## Example queries

Stale data / collector health:

```
time() - dependency_track_exporter_last_success_timestamp_seconds > 900
```

```
rate(dependency_track_exporter_collection_errors_total[15m]) > 0
```

`WARN` policy violations not analysed or suppressed, prod projects only:

```
(dependency_track_project_policy_violations{state="WARN",analysis!="APPROVED",analysis!="REJECTED",suppressed="false"} > 0)
  * on (uuid) group_left(tags,active) dependency_track_project_info{active="true",tags=~".*,prod,.*"}
```

Portfolio critical + KEV exposure:

```
dependency_track_portfolio_vulnerabilities{severity="CRITICAL"}
dependency_track_portfolio_kev
```

## Grafana

A starter dashboard is in [`dashboards/dependency-track.json`](dashboards/dependency-track.json)
(portfolio severity breakdown, top-N projects by inherited risk score, policy
violation backlog, and exporter freshness).

## Development

```
go test ./...
go build ./cmd/dependency-track-exporter
```

Mock Dependency-Track responses for both API versions live in
`internal/dtrack/testdata/{v1,v2}`.

## Changelog

See [CHANGELOG.md](CHANGELOG.md).

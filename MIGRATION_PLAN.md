# dependency-track-exporter — Fork & Modernization Plan (Dependency-Track 5.x)

## 1. Analysis of the existing exporter

### 1.1 Architecture (as of upstream `b4b3d3c`)

| File | Responsibility |
|---|---|
| `main.go` | flag/env parsing (`kingpin`), builds a `dtrack.Client` with `WithAPIKey`, wires `exporter.Exporter.HandlerFunc()` onto `--web.metrics-path`, starts `exporter-toolkit/web` server, waits for SIGTERM. |
| `internal/exporter/exporter.go` | All collection logic. One `Exporter{Client, Logger}` struct. `HandlerFunc` builds a **fresh `prometheus.NewRegistry()` per scrape** and runs `collectPortfolioMetrics` + `collectProjectMetrics` **synchronously against the DT API**, returns HTTP 500 if either fails. |
| `internal/exporter/exporter_test.go` | Two tests, both only exercise pagination of `fetchProjects` / `fetchPolicyViolations` against an `httptest` mock of `/api/v1/*`. |
| `Dockerfile` / `Dockerfile.goreleaser` | `golang:1.20-buster` build → `distroless/static:nonroot`, `USER nonroot`, `EXPOSE 9916`. |
| `.github/workflows/` | `test-and-build.yaml` (go test + goreleaser snapshot on PR), `release.yaml` (goreleaser on tag → ghcr.io/jetstack). |

### 1.2 Data sources per scrape

| Call | Endpoint | Pagination |
|---|---|---|
| `Client.Metrics.LatestPortfolioMetrics` | `GET /api/v1/metrics/portfolio/current` | single request |
| `fetchProjects` → `Project.GetAll` | `GET /api/v1/project?pageNumber=N&pageSize=50` | **`dtrack.FetchAll`, hard-coded `pageSize = 50`** |
| `fetchPolicyViolations` → `PolicyViolation.GetAll` | `GET /api/v1/violation?suppressed=true&pageNumber=N&pageSize=50` | **`dtrack.FetchAll`, hard-coded `pageSize = 50`** |

Project objects already carry an embedded `metrics` object, so per-project metrics do **not** cost an extra call — the cost is `ceil(projects/50) + ceil(violations/50) + 1` requests **on every `/metrics` scrape**. On a portfolio with 3k projects and 20k violations that is ~460 sequential DT API calls per scrape, each of which can now hit DT 5.1's enforced DB query timeout on `/api/v1/violation`.

### 1.3 Existing metric names (MUST be preserved)

```
dependency_track_portfolio_inherited_risk_score
dependency_track_portfolio_vulnerabilities{severity}
dependency_track_portfolio_findings{audited}
dependency_track_project_info{uuid,name,version,classifier,active,tags}
dependency_track_project_vulnerabilities{uuid,name,version,severity}
dependency_track_project_policy_violations{uuid,name,version,type,state,analysis,suppressed}
dependency_track_project_last_bom_import{uuid,name,version}
dependency_track_project_inherited_risk_score{uuid,name,version}
```

### 1.4 Findings on the upstream `DependencyTrack/client-go` library

- Latest is **v0.19.0** (repo currently pins v0.11.0).
- **No `/api/v2` support and no `next_page_token` pagination** in any released version — `withPageOptions` only emits `offset` / `pageNumber` / `pageSize`, and `dtrack.ForEach` hard-codes `pageSize = 50`.
- v0.19.0 adds useful **types**: `Project.IsLatest *bool` (DT ≥ 4.12), `Project.CollectionLogic` / `CollectionTag` (DT ≥ 4.13), plus `Health`, `Config`, `ACL` services.
- `PortfolioMetrics` / `ProjectMetrics` structs expose critical/high/medium/low/unassigned, vulnerabilities, suppressed, findings{Total,Audited,Unaudited}, policyViolations{Total,Fail,Warn,Info,Audited,Unaudited} and the Security/License/Operational breakdowns — but **no KEV field**.

**Conclusion:** to satisfy "prefer REST API v2 + token pagination + max page size" we cannot rely on `client-go` alone. We add a **thin internal DT client** (`internal/dtrack`) that wraps `client-go` for v1 types/fallback and implements v2 + token pagination + tunable page size ourselves.

---

## 2. Target architecture

```
cmd/dependency-track-exporter/main.go     # was ./main.go — flags, wiring, graceful shutdown
internal/config/config.go                 # typed config + validation, env + flags, redaction
internal/dtrack/                           # thin DT API client
    client.go                             #   http.Client w/ timeout, retry (cenkalti/backoff), UA, API-key transport (never logged)
    v1.go                                  #   v1 endpoints (wraps DependencyTrack/client-go where convenient)
    v2.go                                  #   v2 endpoints + next_page_token pagination
    pagination.go                          #   generic paginators: offsetPager (v1), tokenPager (v2)
    capabilities.go                        #   probe /api/version + /api/v2 to decide v1/v2/auto per resource
    types.go                              #   Snapshot input DTOs (Project, PortfolioMetrics, Violation, Finding)
internal/collector/
    collector.go                          #   background loop: ticker(refreshInterval), context, single-flight
    snapshot.go                            #   immutable PortfolioSnapshot value type (all raw numbers + project list)
    store.go                              #   atomic.Pointer[PortfolioSnapshot]; keeps last-good on failure
internal/exporter/
    exporter.go                           #   prometheus.Collector that renders the cached Snapshot on Collect()
    selfmetrics.go                        #   exporter self-metrics (duration, errors_total, last_success, cache_age)
    describe.go                           #   metric descriptors + name/label compatibility table
internal/exporter/testdata/
    v1/*.json  v2/*.json                   #   recorded mock API responses
```

### 2.1 Request flow

- **Startup:** `main` builds config → `dtrack.Client` → `collector.Collector` → runs one **synchronous initial collection** (so `/metrics` is meaningful immediately; failure is logged but non-fatal, `/metrics` then serves zero-value self-metrics until first success) → starts background goroutine → registers `exporter` + self-metrics on the global registry → starts HTTP server.
- **Background collector:** every `refreshInterval` (default `5m`, flag `--dtrack.refresh-interval`): build a new `Snapshot` from DT, time it, on success `store.Set(snapshot)` and update self-metrics; on failure increment `collection_errors_total`, log, **keep previous snapshot**.
- **`/metrics`:** `promhttp` handler over the global registry. `exporter.Collect()` reads `store.Get()` (an `atomic.Pointer` load — no locks, no DT calls) and emits gauges. Always fast, always 200.
- **`/healthz`:** 200 always. **`/readyz`:** 200 only once `store` has a snapshot.

### 2.2 Concurrency / safety

- `store` = `atomic.Pointer[Snapshot]`; readers never block writers.
- Collector uses its own `context.WithTimeout(ctx, min(refreshInterval*0.9, collectionTimeout))`.
- Optional bounded concurrency (`--dtrack.max-concurrency`, default 4) for any per-project fan-out calls (v2 KEV fallback etc.).
- Single-flight guard so a slow collection can't overlap the next tick.

---

## 3. Dependency-Track v1 → v2 mapping

| Data | v1 (fallback) | v2 (preferred) | Pagination |
|---|---|---|---|
| Portfolio metrics | `GET /api/v1/metrics/portfolio/current` | *(no v2 equivalent yet)* → **stay v1** | single |
| Projects + embedded metrics | `GET /api/v1/project?pageSize=…` | `GET /api/v2/projects?limit=…&pageToken=…` where available | v1 offset / **v2 token** |
| Policy violations | `GET /api/v1/violation?suppressed=true&pageSize=…` | v2 if present, else v1 | v1 offset / **v2 token** |
| Findings totals | project metrics (`findingsTotal/Audited/Unaudited`) already cover it | same | — |
| KEV counts | not in metrics struct → `GET /api/v1/vulnerability` filtered / project findings `attribution`+`vulnerability.kev`; **fallback: KEV metric emitted only when derivable, else omitted** | v2 finding fields if exposed | token/offset |

- **Page size:** `--dtrack.page-size` default **`100`**, clamped to `[1, 500]` (DT rejects very large pages; 100 is the practical sweet spot and 5–10× fewer requests than today).
- **Token pagination:** `tokenPager` loops `pageToken=""` → response `{items, next_page_token}` until `next_page_token` empty. `offsetPager` loops `pageNumber` using `X-Total-Count` (today's behavior) but with the configurable page size.
- **`auto` mode (default):** on startup probe `GET /api/version` and a cheap `GET /api/v2/...?limit=1`. Cache per-resource decision. `--dtrack.api-version=v1|v2|auto`.

---

## 4. Metrics — final set

### 4.1 Preserved (unchanged names & labels)
All eight metrics from §1.3, byte-for-byte. `project_info` keeps `{uuid,name,version,classifier,active,tags}` and **adds** `latest` (from `IsLatest`) — additive labels are backward compatible for existing queries.

### 4.2 New portfolio metrics
```
dependency_track_portfolio_suppressed
dependency_track_portfolio_projects
dependency_track_portfolio_components
dependency_track_portfolio_vulnerable_projects
dependency_track_portfolio_vulnerable_components
dependency_track_portfolio_findings_total
dependency_track_portfolio_policy_violations{state="FAIL|WARN|INFO"}
dependency_track_portfolio_policy_violations_by_class{class="SECURITY|LICENSE|OPERATIONAL",audited="true|false"}
dependency_track_portfolio_kev            # only when derivable from the DT version in use
```
(`portfolio_vulnerabilities{severity}` already covers critical/high/medium/low/unassigned; KEV added as `severity="KEV"` **and** the standalone gauge for convenience.)

### 4.3 New project metrics (labels: `uuid,name,version` unless noted)
```
dependency_track_project_suppressed
dependency_track_project_components
dependency_track_project_findings{audited="true|false"}
dependency_track_project_findings_total
dependency_track_project_policy_violations_total{type,state}          # FAIL/WARN/INFO counts from metrics
dependency_track_project_kev
dependency_track_project_last_bom_import        # preserved
dependency_track_project_inherited_risk_score   # preserved
```
`project_policy_violations` (the detailed `{type,state,analysis,suppressed}` series built from `/violation`) is preserved exactly, still with the 0-initialization of all label combinations.

### 4.4 Exporter self-metrics (new, namespace `dependency_track_exporter`)
```
dependency_track_exporter_collection_duration_seconds        gauge  (last run)
dependency_track_exporter_collection_duration_seconds_total  + _count  (histogram optional, v2)
dependency_track_exporter_collection_errors_total            counter
dependency_track_exporter_last_success_timestamp_seconds     gauge
dependency_track_exporter_cache_age_seconds                  gauge (computed at scrape = now - last_success)
dependency_track_exporter_build_info                         (kept from version.NewCollector)
```

---

## 5. Config surface (flags + env, all env-overridable)

| Flag | Env | Default | Notes |
|---|---|---|---|
| `--dtrack.address` | `DEPENDENCY_TRACK_ADDR` | `http://localhost:8080` | |
| `--dtrack.api-key` | `DEPENDENCY_TRACK_API_KEY` | *(required)* | **never logged**; redacted to `***` in all log lines and errors |
| `--dtrack.api-version` | `DEPENDENCY_TRACK_API_VERSION` | `auto` | `v1\|v2\|auto` |
| `--dtrack.refresh-interval` | `DEPENDENCY_TRACK_REFRESH_INTERVAL` | `5m` | background collection cadence |
| `--dtrack.timeout` | `DEPENDENCY_TRACK_TIMEOUT` | `30s` | per-HTTP-request timeout |
| `--dtrack.collection-timeout` | | `4m` | whole-collection deadline |
| `--dtrack.retry-max` | | `3` | retries on 5xx / timeout / connection errors (exp. backoff w/ jitter) |
| `--dtrack.retry-base-delay` | | `500ms` | |
| `--dtrack.page-size` | | `100` | clamped `[1,500]` |
| `--dtrack.max-concurrency` | | `4` | per-project fan-out bound |
| `--dtrack.tls-insecure-skip-verify` | | `false` | |
| `--web.listen-address` / `--web.metrics-path` / `--web.config.file` | | `:9916` / `/metrics` | unchanged |
| `--log.level` / `--log.format` | | `info` / `logfmt` | unchanged |

API key redaction: wrap the key in a `type Secret string` whose `String()`/`MarshalText()` returns `***`; the key only ever reaches the `authHeaderTransport`.

---

## 6. Testing plan

- `internal/dtrack`: table tests against `httptest.Server` serving `testdata/v1/*.json` and `testdata/v2/*.json`.
  - offset pagination across N pages with configurable page size (port existing 468-item test, parametrized on pageSize).
  - token pagination: server emits `next_page_token` for 3 pages then empty.
  - retry: server returns `503,503,200` → succeeds in 3 tries; returns `503×4` → error after `retry-max`.
  - timeout: server sleeps > `--dtrack.timeout` → context deadline error.
  - `auto` capability probe: `/api/version` + `/api/v2` present → v2 chosen; 404 → v1.
  - **API key redaction test:** assert key never appears in captured log buffer / error strings; assert `X-Api-Key` header IS sent.
- `internal/collector`: fake `dtrack` client (interface) →
  - happy path populates snapshot;
  - second refresh fails → `store.Get()` still returns first snapshot, `errors_total == 1`, `last_success` unchanged;
  - `cache_age_seconds` increases with a fake clock.
- `internal/exporter`: golden-file test using `prometheus/testutil.CollectAndCompare` against a `metrics.golden` fixture — **this is the backward-compat guard** for metric names/labels.
- Keep `-short` fast; mark any slow test with `testing.Short()`.

---

## 7. Packaging / CI

- **Dockerfile:** multi-stage, `golang:1.23` (or `1.24`) → `gcr.io/distroless/static:nonroot`. Add `USER 65532:65532` numeric (rootless Podman/Quadlet friendliness), `--chmod` on copy, no writable dirs needed. Keep `EXPOSE 9916`. Add `HEALTHCHECK` via `/healthz` (wget-less: rely on distroless — document `podman healthcheck` using the endpoint instead). Add OCI labels.
- **Quadlet:** ship `packaging/dependency-track-exporter.container` example (`User=`, `NoNewPrivileges=true`, `ReadOnlyRootFilesystem` compatible, env file for the API key).
- **GitHub Actions:**
  - `test-and-build.yaml`: bump `actions/checkout@v4`, `setup-go@v5`, Go `1.23`, add `go vet`, `golangci-lint`, `go test -race -covermode=atomic ./...`, build multi-arch image with `docker/build-push-action` (no push on PR).
  - `release.yaml`: bump actions, goreleaser v2 config (`.goreleaser.yaml` → `version: 2`, replace deprecated `--rm-dist`/`--skip-*` flags), keep ghcr multi-arch manifest. Parametrize `owner` for the fork.
  - Add `dependabot.yml` gomod + actions + docker.
- `go.mod` → `go 1.23`, bump `client-go` to v0.19.0, `prometheus/client_golang` latest, add `github.com/cenkalti/backoff/v4`.

---

## 8. Incremental delivery (each step compiles + tests green)

1. **Scaffold + module rename** — move `main.go`→`cmd/…`, add `internal/config`, bump `go.mod`, CI Go version. No behavior change.
2. **`internal/dtrack` v1 client** with configurable page size + timeout + retry; port existing pagination tests. Exporter still synchronous but via new client.
3. **`internal/collector` + `store`** + self-metrics; `/metrics` switches to cached snapshot; add `/healthz` `/readyz`. **This is the performance fix** — ship-worthy on its own.
4. **Preserve-compat golden test** for all existing metric names.
5. **New portfolio + project metrics** from data already in the snapshot (no new API calls).
6. **`internal/dtrack` v2 + token pagination + capability probe**; `--dtrack.api-version`.
7. **KEV + fallbacks** for metrics not in v2/metrics structs.
8. **Dockerfile + Quadlet + CI/goreleaser** modernization.
9. **README** rewrite: DT 5.x setup, required permissions (`VIEW_PORTFOLIO`, `VIEW_POLICY_VIOLATION`, `VIEW_VULNERABILITY`), full metric table, Prometheus scrape config (`scrape_interval: 60s`, `scrape_timeout: 30s` now safe), example alert rules, and a Grafana dashboard JSON in `dashboards/`.

---

## 9. Open questions for you

1. **Fork identity** — module path / GHCR owner (e.g. `github.com/<you>/dependency-track-exporter`)? Keep binary name `dependency-track-exporter`.
2. **DT version floor** — target DT 4.12+ (needs `isLatest`) or also support 4.10/4.11 (label emitted empty)?
3. **API v2 reality check** — do you have a DT 5.1 instance I should probe for the actual `/api/v2` surface + `openapi.json`? The released `client-go` has zero v2 code, so the v2 layer is written blind against docs unless we can introspect a live server.
4. **KEV source** — is KEV data present in your DT metrics payload (some builds add `vulnerabilitiesKev`), or should the exporter derive it from findings?
5. Keep `exporter-toolkit` web server (TLS/basic-auth via `--web.config.file`) — yes, assumed.

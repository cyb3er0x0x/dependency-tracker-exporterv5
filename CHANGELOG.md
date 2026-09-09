# Changelog

All notable changes to this project are documented here. The format is based on
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and this project adheres
to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Changed
- `go` directive bumped to `1.24`; CI `setup-go` to `1.24.x`; Docker build
  image to `golang:1.27-bookworm`.
- `golangci-lint` upgraded to v2 (`golangci-lint-action@v9`, pinned
  `v2.13.2`); `.golangci.yml` migrated to the v2 schema.
- Dependency updates: `actions/upload-artifact` v7, `docker/setup-qemu-action`
  v4, `docker/setup-buildx-action` v4, `goreleaser/goreleaser-action` v7.
- Maximum `--dtrack.page-size` lowered from 500 to **100**. Dependency-Track
  does not signal when it caps an oversized page server-side (which would
  silently truncate results), and DT 5.x's database query timeout makes large
  pages fragile. 100 is DT's own default.
- `dependency_track_project_policy_violations_total` labels reduced to
  `{state}`; per-class counts moved to a new
  `dependency_track_project_policy_violations_by_class{class,audited}` metric,
  mirroring the portfolio metrics. (These metrics are new in this line; no
  released version exposed the old shape.)

### Added
- `.golangci.yml` with an `errcheck` exclusion for `go-kit/log`'s
  `Logger.Log`.

### Fixed
- **v1 pagination could silently truncate** when Dependency-Track returned
  fewer rows than the requested page size: the walk now treats `X-Total-Count`
  as authoritative and only stops on a short page when that header is absent.
- Token and offset pagination are now bounded, so a misbehaving server cannot
  cause an unbounded loop.
- A panic while decoding or rendering a Dependency-Track response no longer
  kills the background refresh goroutine; it is counted as a collection error
  and the previous snapshot is retained.
- Exponential-backoff retry delay is capped (was unbounded for large
  `--dtrack.retry-max`).
- Removed the unimplemented `--dtrack.max-concurrency` flag.
- CI `golangci-lint` step failures (`errcheck` on unchecked `Logger.Log`
  and `resp.Body.Close`; `revive` unused parameter).

## [0.1.1] - 2026-09-09

### Added
- `CHANGELOG.md`.
- Versioned install instructions in the README: container image with a pinned
  release tag, pre-built binary download from the releases page, and
  `go install` from source.

## [0.1.0] - 2026-09-09

First release of the modernized fork of
[`jetstack/dependency-track-exporter`](https://github.com/jetstack/dependency-track-exporter),
targeting OWASP Dependency-Track 4.12+ / 5.x. Retains the upstream Apache-2.0
license.

### Added
- **Background collector** — Dependency-Track is queried on a configurable
  interval (`--dtrack.refresh-interval`, default `5m`) instead of on every
  scrape. The latest successful portfolio snapshot is held in memory and served
  to `/metrics` immediately.
- **Last-good snapshot retention** — a failed refresh keeps the previous
  snapshot; `/metrics` never fails because of Dependency-Track.
- **REST API v2 support** with `next_page_token` pagination and a startup
  capability probe (`--dtrack.api-version=v1|v2|auto`, default `auto`), falling
  back to v1 per resource.
- **Configurable request behaviour** — `--dtrack.page-size` (default `100`,
  clamped `[1,100]`, was a hard-coded `50`), `--dtrack.timeout`,
  `--dtrack.collection-timeout`, `--dtrack.retry-max`,
  `--dtrack.retry-base-delay`,
  `--dtrack.tls-insecure-skip-verify`.
- **Exporter self-metrics**: `dependency_track_exporter_collection_duration_seconds`,
  `_collection_errors_total`, `_last_success_timestamp_seconds`,
  `_cache_age_seconds`.
- **New portfolio metrics**: `_suppressed`, `_projects`, `_components`,
  `_vulnerable_projects`, `_vulnerable_components`, `_findings_total`, `_kev`,
  `_policy_violations{state}`, `_policy_violations_by_class{class,audited}`.
- **New project metrics**: `_findings{audited}`, `_findings_total`, `_suppressed`,
  `_components`, `_kev`, `_policy_violations_total{type,state}`.
- `/healthz` (liveness) and `/readyz` (ready once the first snapshot exists)
  endpoints.
- Rootless Podman / Quadlet unit in `packaging/`.
- Starter Grafana dashboard in `dashboards/`.
- Unit tests with mock v1 and v2 API responses under
  `internal/dtrack/testdata/`.

### Changed
- API key is wrapped in a redacting `Secret` type — it is never written to logs
  or error messages.
- `/metrics` no longer performs synchronous, per-scrape paginated
  Dependency-Track requests.
- `dependency_track_project_info` gains an additive `latest` label (from the
  Dependency-Track `isLatest` field). Existing metric names and label sets are
  otherwise unchanged.
- Repository restructured into `cmd/` + `internal/{config,dtrack,collector,exporter}`.
- Go 1.23; dependency on `DependencyTrack/client-go` dropped in favour of an
  internal client.
- Dockerfile rebuilt on `golang:1.23` → `distroless/static`, running as numeric
  UID `65532` with a read-only-compatible root filesystem.
- CI moved to `actions/setup-go@v5`, `golangci-lint`, `go test -race`, and
  GoReleaser v2.

[Unreleased]: https://github.com/cyb3er0x0x/dependency-tracker-exporterv5/compare/v0.1.1...HEAD
[0.1.1]: https://github.com/cyb3er0x0x/dependency-tracker-exporterv5/compare/v0.1.0...v0.1.1
[0.1.0]: https://github.com/cyb3er0x0x/dependency-tracker-exporterv5/releases/tag/v0.1.0

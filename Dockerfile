# syntax=docker/dockerfile:1
FROM golang:1.27-bookworm AS build

WORKDIR /src

COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download

COPY . .

ARG VERSION=dev
ARG REVISION=unknown
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -trimpath \
      -ldflags="-s -w \
        -X github.com/prometheus/common/version.Version=${VERSION} \
        -X github.com/prometheus/common/version.Revision=${REVISION}" \
      -o /out/dependency-track-exporter ./cmd/dependency-track-exporter

FROM gcr.io/distroless/static:nonroot

# Numeric UID/GID so rootless Podman / Quadlet can map it without /etc/passwd.
USER 65532:65532

COPY --from=build --chown=65532:65532 /out/dependency-track-exporter /dependency-track-exporter

EXPOSE 9916

ENTRYPOINT ["/dependency-track-exporter"]

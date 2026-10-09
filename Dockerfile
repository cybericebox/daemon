# syntax=docker/dockerfile:1
# AP Backend — pure auth/API service. Same build shape as the legacy daemon's
# deploy/Dockerfile (static binary + migrations copied into a slim alpine image).
# Migrations run on boot via golang-migrate from file://migrations, so the
# migrations directory must sit next to the binary at WORKDIR (/app/migrations).
# Boot uses ENV != "development" so the in-app MigrationsPath resolves to
# "migrations" (relative to /app), not the source-tree path.
# The build runs on the build platform and cross-compiles (pure Go, CGO off): arm64 images need no emulation.
ARG LABORATORY_CONTRACT_SOURCE=50b0e26b9e4b579d9774ba9d13f90189568defd2
FROM --platform=$BUILDPLATFORM golang:1.27.1-alpine@sha256:8a5910f31396cd4d89662f56c68b3ae31d374308270a1c3bd96672ee5ed43414 AS builder
ARG LABORATORY_CONTRACT_SOURCE
ARG TARGETOS=linux
ARG TARGETARCH
WORKDIR /build
COPY go.mod go.sum ./
# The relative replace selects a byte-exact scoped SDK without sibling repositories.
COPY third_party/laboratory-sdk ./third_party/laboratory-sdk
RUN cd third_party/laboratory-sdk && sha256sum -c SHA256SUMS && \
    source=$(sed -n 's/.*"SourceCommit": "\([0-9a-f]*\)".*/\1/p' PROVENANCE.json) && \
    test "$source" = "$LABORATORY_CONTRACT_SOURCE"
RUN GOWORK=off go mod download
COPY . .
COPY ./internal/delivery/repository/postgres/migrations /build/migrations
RUN GOWORK=off CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags '-s -w' -o app ./cmd/daemon

FROM gcr.io/distroless/static:nonroot@sha256:e2e927ec666bae08560abb3c55d0659eceabb657f56b6782ab500a9fc7f555e3
# Runtime Laboratory distribution identity stays separate from SDK source.
ARG LABORATORY_VERSION=unknown
LABEL org.cybericebox.laboratory.commit=$LABORATORY_VERSION
ARG LABORATORY_CONTRACT_SOURCE
LABEL org.cybericebox.laboratory.contract.source=$LABORATORY_CONTRACT_SOURCE
# The daemon runs as an unprivileged user (UID 10001, no root inside the container; the numeric USER needs no passwd
# entry). Without TLS material it listens on plain 8080 (HTTP_SERVER_PORT changes it). With /tls/tls.crt and
# /tls/tls.key mounted it serves HTTPS on 8443 only, and with /aop/ca.crt too it requires client certificates:
# the deploy passes none of these. The health listener is on 8081.
ENV HTTP_SERVER_HEALTH_PORT=8081
WORKDIR /app

# db migration files (applied on boot)
COPY --from=builder /build/migrations /app/migrations

# the built binary
COPY --from=builder /build/app /app/app

USER 10001:10001
ENTRYPOINT ["/app/app"]
# 8080: plain HTTP (no TLS files); 8443: HTTPS (/tls/tls.crt, /tls/tls.key); 8081: the health port (HTTP_SERVER_HEALTH_PORT).
EXPOSE 8080 8443 8081

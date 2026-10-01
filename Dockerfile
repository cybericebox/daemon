# syntax=docker/dockerfile:1
# AP Backend — pure auth/API service. Same build shape as the legacy daemon's
# deploy/Dockerfile (static binary + migrations copied into a slim alpine image).
# Migrations run on boot via golang-migrate from file://migrations, so the
# migrations directory must sit next to the binary at WORKDIR (/app/migrations).
# Boot uses ENV != "development" so the in-app MigrationsPath resolves to
# "migrations" (relative to /app), not the source-tree path.
# The build runs on the build platform and cross-compiles (pure Go, CGO off): arm64 images need no emulation.
FROM --platform=$BUILDPLATFORM golang:1.27.1-alpine AS builder
ARG TARGETOS=linux
ARG TARGETARCH
WORKDIR /build
RUN apk add --no-cache gcc g++
COPY go.* ./
# The backend imports the Laboratory agent contract through the local Go-module
# replacement in go.mod.  Docker build contexts are isolated, so deployments
# must supply this named context (Compose does so in infrastructure/local).
COPY --from=laboratory go.mod /laboratory/go.mod
# the agent contract and the small packages it and the backend share (vpnprobe, tlsreload, ...)
COPY --from=laboratory pkg /laboratory/pkg
RUN go mod download
COPY . .
COPY ./internal/delivery/repository/postgres/migrations /build/migrations
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -o app -a -ldflags '-w -extldflags "-static"' ./cmd/daemon

FROM alpine
WORKDIR /app

# db migration files (applied on boot)
COPY --from=builder /build/migrations /app/migrations

# the built binary
COPY --from=builder /build/app /app/app

ENTRYPOINT ["/app/app"]
EXPOSE 8080

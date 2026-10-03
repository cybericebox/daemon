# syntax=docker/dockerfile:1
# AP Backend — pure auth/API service. Same build shape as the legacy daemon's
# deploy/Dockerfile (static binary + migrations copied into a slim alpine image).
# Migrations run on boot via golang-migrate from file://migrations, so the
# migrations directory must sit next to the binary at WORKDIR (/app/migrations).
# Boot uses ENV != "development" so the in-app MigrationsPath resolves to
# "migrations" (relative to /app), not the source-tree path.
# The build runs on the build platform and cross-compiles (pure Go, CGO off): arm64 images need no emulation.
FROM --platform=$BUILDPLATFORM golang:1.27.1-alpine@sha256:8a5910f31396cd4d89662f56c68b3ae31d374308270a1c3bd96672ee5ed43414 AS builder
ARG TARGETOS=linux
ARG TARGETARCH
WORKDIR /build
COPY go.mod go.sum ./
# The laboratory agent contract comes through go mod download, at the version go.mod pins (no replace, no go.work).
RUN go mod download
COPY . .
COPY ./internal/delivery/repository/postgres/migrations /build/migrations
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -o app -a -ldflags '-w -extldflags "-static"' ./cmd/daemon

FROM alpine:3.24@sha256:294b683cb724975bec92580e1e685676bd4b50bda910ddb8c51d4cabeaec77e6
# The laboratory version go.mod pins (CI passes it). infrastructure checks it against LABORATORY_IMAGE_TAG.
ARG LABORATORY_VERSION=unknown
LABEL org.cybericebox.laboratory.commit=$LABORATORY_VERSION
# The daemon runs as an unprivileged user (no root inside the container). It therefore listens on 8080;
# set HTTP_SERVER_PORT to change it.
RUN addgroup -S -g 10001 app && adduser -S -D -u 10001 -G app app
ENV HTTP_SERVER_PORT=8080
WORKDIR /app

# db migration files (applied on boot)
COPY --from=builder /build/migrations /app/migrations

# the built binary
COPY --from=builder /build/app /app/app

USER 10001:10001
ENTRYPOINT ["/app/app"]
EXPOSE 8080

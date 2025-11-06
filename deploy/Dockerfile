FROM golang:1.25.3-alpine AS builder
WORKDIR /build
RUN apk add gcc g++ --no-cache
COPY go.* ./
RUN go mod download
COPY . .
COPY ./internal/delivery/repository/postgres/migrations /build/migrations
RUN CGO_ENABLED=0 GOOS=linux go build -o daemon -a -ldflags '-w -extldflags "-static"' /build/cmd/main.go

FROM alpine
WORKDIR /app

# copy db migration files
COPY --from=builder /build/migrations /app/migrations

# copy the built binary into the image
COPY --from=builder /build/daemon /app/daemon

ENTRYPOINT ["/app/daemon"]
EXPOSE 80
EXPOSE 443

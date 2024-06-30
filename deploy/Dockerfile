FROM golang:1.22.3-alpine AS builder
WORKDIR /build
RUN apk add gcc g++ --no-cache
COPY go.* ./
RUN go mod download
COPY . .
COPY ./internal/delivery/repository/postgres/migrations /build/migrations
COPY ./deploy/nginx.conf /build/nginx.conf
RUN CGO_ENABLED=0 GOOS=linux go build -o app -a -ldflags '-w -extldflags "-static"' /build/cmd/main.go

FROM nginx:1.24.3-alpine
WORKDIR /app

# copy the built binary into the image
COPY --from=builder /build/app /app/app

# copy db migration files
COPY --from=builder /build/migrations /app/migrations

# copy nginx configuration
COPY --from=builder /build/nginx.conf /etc/nginx/conf.d/default.conf

ENTRYPOINT ["/app/app"]
EXPOSE 80
EXPOSE 443
EXPOSE 51820/udp
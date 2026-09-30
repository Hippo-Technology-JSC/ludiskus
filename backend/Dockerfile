# syntax=docker/dockerfile:1
# Multi-stage build cho ludiskus backend (docs/12).
# api và worker dùng chung một image, chọn binary qua LUDISKUS_ROLE.

FROM golang:1.26-alpine AS toolchain
WORKDIR /src
ENV CGO_ENABLED=0
# Development: chạy bằng go run (root compose mount source + override command)
FROM toolchain AS development
CMD ["go", "run", "./cmd/api"]

FROM toolchain AS base
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download

FROM base AS build
COPY . .
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build go build -trimpath -ldflags="-s -w" -o /out/ludiskus ./cmd/api && \
    go build -trimpath -ldflags="-s -w" -o /out/ludiskus-worker ./cmd/worker

FROM alpine:3.21 AS production
RUN apk add --no-cache ca-certificates
COPY --from=build /out/ /usr/local/bin/
COPY deploy/entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh
EXPOSE 8080
ENTRYPOINT ["/entrypoint.sh"]

# Multi-stage build cho ludiskus backend (docs/12).
# api và worker dùng chung một image, chọn binary qua LUDISKUS_ROLE.

FROM golang:1.26-alpine AS base
WORKDIR /src
ENV CGO_ENABLED=0
COPY go.mod go.sum ./
RUN go mod download
COPY . .

# Development: chạy bằng go run (root compose mount source + override command)
FROM base AS development
CMD ["go", "run", "./cmd/api"]

FROM base AS build
RUN go build -trimpath -ldflags="-s -w" -o /out/ludiskus ./cmd/api && \
    go build -trimpath -ldflags="-s -w" -o /out/ludiskus-worker ./cmd/worker

FROM alpine:3.21 AS production
RUN apk add --no-cache ca-certificates
COPY --from=build /out/ /usr/local/bin/
COPY deploy/entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh
EXPOSE 8080
ENTRYPOINT ["/entrypoint.sh"]

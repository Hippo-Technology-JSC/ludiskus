# Build the license-free MinIO Community Edition and client from pinned source tags.
ARG GO_VERSION=1.24.8

FROM golang:${GO_VERSION}-alpine AS minio-builder
ARG MINIO_VERSION=RELEASE.2025-10-15T17-29-55Z
ENV CGO_ENABLED=0
RUN apk add --no-cache ca-certificates
RUN go install -ldflags="-X github.com/minio/minio/cmd.Version=2025-10-15T17:29:55Z -X github.com/minio/minio/cmd.ReleaseTag=RELEASE.2025-10-15T17-29-55Z" github.com/minio/minio@${MINIO_VERSION}

FROM alpine:3.22 AS minio
RUN apk add --no-cache ca-certificates
COPY --from=minio-builder /go/bin/minio /usr/local/bin/minio
EXPOSE 9000 9001
VOLUME ["/data"]
ENTRYPOINT ["minio"]
CMD ["server", "/data", "--console-address", ":9001"]

FROM golang:${GO_VERSION}-alpine AS mc-builder
ARG MC_VERSION=RELEASE.2025-08-13T08-35-41Z
ENV CGO_ENABLED=0
RUN apk add --no-cache ca-certificates
RUN go install github.com/minio/mc@${MC_VERSION}

FROM alpine:3.22 AS mc
RUN apk add --no-cache ca-certificates
COPY --from=mc-builder /go/bin/mc /usr/local/bin/mc
ENTRYPOINT ["mc"]

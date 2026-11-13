# syntax=docker/dockerfile:1

# ---------------------------------------------------------------------------
# Build stage: pure-Go (modernc.org/sqlite), so no C toolchain and CGO is off.
# ---------------------------------------------------------------------------
FROM golang:1.23-alpine AS build

WORKDIR /src

# Download modules first so this layer is reused when only source files change.
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download

COPY . .

# TARGETOS/TARGETARCH are filled in by docker buildx; they stay empty for a
# plain "docker build", which then builds for the host architecture.
ARG TARGETOS
ARG TARGETARCH

RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS="${TARGETOS:-linux}" GOARCH="${TARGETARCH:-amd64}" \
    go build -trimpath -ldflags='-s -w' -o /out/yoloapi ./cmd/server

# ---------------------------------------------------------------------------
# Runtime stage: small Alpine image running as a non-root user.
# ---------------------------------------------------------------------------
FROM alpine:3.21 AS runtime

# Nothing to install: the app makes no outbound requests (so no CA bundle is
# needed), stores all timestamps in UTC (so no tzdata is needed), and Alpine's
# busybox already provides wget for the health check below.
RUN addgroup -g 10001 -S yolo \
    && adduser -u 10001 -S -G yolo yolo

WORKDIR /app

COPY --from=build /out/yoloapi /usr/local/bin/yoloapi

# All state lives in DATA_DIR: the SQLite database, project images, and the
# generated dataset jobs. Mount a volume here to keep it between containers.
RUN mkdir -p /data && chown -R yolo:yolo /data /app

USER 10001:10001

ENV ADDR=":8080" \
    DATA_DIR="/data" \
    MAX_UPLOAD_MB="512"

EXPOSE 8080

VOLUME ["/data"]

HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
    CMD wget -qO- "http://127.0.0.1${ADDR}/health" >/dev/null 2>&1 || exit 1

LABEL org.opencontainers.image.title="YOLO Dataset API" \
    org.opencontainers.image.description="YOLO dataset splitting API and annotation workspace"

# Set the public source URL when publishing, e.g.
#   docker build --build-arg SOURCE_REPO=https://github.com/<you>/<repo>.git .
ARG SOURCE_REPO=local/yoloapi
ARG VERSION=dev
ARG COMMIT=unknown
LABEL org.opencontainers.image.source="${SOURCE_REPO}" \
    org.opencontainers.image.version="${VERSION}" \
    org.opencontainers.image.revision="${COMMIT}"

ENTRYPOINT ["/usr/local/bin/yoloapi"]

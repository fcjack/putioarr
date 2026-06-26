FROM golang:1.26 AS builder

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY VERSION ./
COPY ./cmd/seedbox_downloader ./cmd/seedbox_downloader
COPY ./internal ./internal

ARG COMMIT=unknown
ARG BUILD_TIME=unknown
ARG VERSION

RUN set -eux; \
    if [ -z "${VERSION}" ]; then VERSION="$(tr -d '[:space:]' < VERSION)"; fi; \
    CGO_ENABLED=1 GOOS=linux go build -trimpath \
      -ldflags="-s -w \
        -X github.com/italolelis/seedbox_downloader/internal/version.Version=${VERSION} \
        -X github.com/italolelis/seedbox_downloader/internal/version.Commit=${COMMIT} \
        -X github.com/italolelis/seedbox_downloader/internal/version.BuildTime=${BUILD_TIME}" \
      -o seedbox_downloader ./cmd/seedbox_downloader/main.go

# Create /config and set correct permissions for non-root user
RUN mkdir -p /config

FROM gcr.io/distroless/cc:nonroot

WORKDIR /app

LABEL org.opencontainers.image.title="putioarr" \
      org.opencontainers.image.description="Sync files from remote download clients"

# Copy /config from builder stage
COPY --from=builder --chown=65532:65532 /config /config

COPY --from=builder /app/seedbox_downloader .

ENTRYPOINT ["/app/seedbox_downloader"]

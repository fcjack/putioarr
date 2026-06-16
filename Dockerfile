FROM golang:1.26 AS builder

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY ./cmd/seedbox_downloader ./cmd/seedbox_downloader
COPY ./internal ./internal

RUN CGO_ENABLED=1 GOOS=linux go build -trimpath -ldflags="-s -w" -ldflags "-X main.version=${VERSION}" -o seedbox_downloader ./cmd/seedbox_downloader/main.go

# Create /config and set correct permissions for non-root user
RUN mkdir -p /config

FROM gcr.io/distroless/cc:nonroot

WORKDIR /app

# Copy /config from builder stage
COPY --from=builder --chown=65532:65532 /config /config

COPY --from=builder /app/seedbox_downloader .

ENTRYPOINT ["/app/seedbox_downloader"]

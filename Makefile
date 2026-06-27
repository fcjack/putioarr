.PHONY: build build-all frontend frontend-install test version

VERSION ?= $(shell tr -d '[:space:]' < VERSION)
COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
BUILD_TIME ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)

LDFLAGS := -s -w \
	-X github.com/italolelis/seedbox_downloader/internal/version.Version=$(VERSION) \
	-X github.com/italolelis/seedbox_downloader/internal/version.Commit=$(COMMIT) \
	-X github.com/italolelis/seedbox_downloader/internal/version.BuildTime=$(BUILD_TIME)

# Build the backend only. Embeds whatever is currently in internal/http/ui/dist
# (run `make frontend` first, or `make build-all`, to embed a fresh SPA build).
build:
	CGO_ENABLED=1 go build -trimpath -ldflags "$(LDFLAGS)" -o bin/seedbox_downloader ./cmd/seedbox_downloader

# Build the Vue SPA into the Go embed directory (requires Node + npm).
frontend:
	cd web && npm install && npm run build

# Build the SPA and then the backend, producing a binary with the UI embedded.
build-all: frontend build

test:
	go test ./...

version:
	@echo $(VERSION)

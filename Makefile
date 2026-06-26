.PHONY: build test version

VERSION ?= $(shell tr -d '[:space:]' < VERSION)
COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
BUILD_TIME ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)

LDFLAGS := -s -w \
	-X github.com/italolelis/seedbox_downloader/internal/version.Version=$(VERSION) \
	-X github.com/italolelis/seedbox_downloader/internal/version.Commit=$(COMMIT) \
	-X github.com/italolelis/seedbox_downloader/internal/version.BuildTime=$(BUILD_TIME)

build:
	CGO_ENABLED=1 go build -trimpath -ldflags "$(LDFLAGS)" -o bin/seedbox_downloader ./cmd/seedbox_downloader

test:
	go test ./...

version:
	@echo $(VERSION)

# Contributing

Contributions are welcome! Please open an issue first for major changes.

```sh
# Run tests
go test -race ./...

# Run linter
golangci-lint run

# Build the Web UI and embed it, then build the binary
make build-all
```

## Frontend development

The Web UI lives in `web/` (Vue 3 + Vite + TypeScript). For a hot-reloading dev server that proxies the API to a locally running backend:

```sh
cd web
npm install
npm run dev
```

`make frontend` builds the production bundle into `internal/http/ui/dist`, where it is embedded into the Go binary via `go:embed`.

## Project Structure

```text
seedbox_downloader/
├── cmd/seedbox_downloader/     # Application entrypoint
├── internal/
│   ├── config/                 # Environment variable loading
│   ├── dc/                     # Download client adapters
│   │   ├── deluge/             #   Deluge JSON-RPC client
│   │   └── putio/              #   Put.io API client
│   ├── downloader/             # Parallel download orchestration
│   │   └── progress/           #   Download progress tracking
│   ├── http/rest/              # Transmission RPC proxy + Web UI REST API
│   ├── http/ui/                # Embedded Vue SPA (go:embed)
│   ├── notifier/               # Discord webhook notifications
│   ├── storage/sqlite/         # SQLite state persistence
│   ├── svc/arr/                # Sonarr/Radarr API clients
│   ├── svc/transfers/          # Web UI read model & actions
│   ├── telemetry/              # OpenTelemetry instrumentation
│   ├── transfer/               # Domain models & orchestrator
│   └── logctx/                 # Structured logging helpers
├── web/                        # Vue 3 + Vite + TypeScript frontend source
├── monitoring/                 # Prometheus + Grafana stack
│   └── grafana/dashboards/     #   Pre-built dashboard
├── Dockerfile                  # Multi-stage distroless build (Node + Go)
├── docker-compose.telemetry.yml
└── .github/workflows/          # CI: lint, test, build, publish
```

## Standards

- **Go version:** 1.26+
- **Linter config:** [`.golangci.yml`](https://github.com/fcjack/putioarr/blob/main/.golangci.yml)
- CI runs lint + tests + race detection on every PR

## License

This project is licensed under the MIT License.

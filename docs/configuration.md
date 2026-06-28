# Configuration

All configuration is done via environment variables.

## Core Settings

| Variable | Default | Description |
|---|---|---|
| `DOWNLOAD_CLIENT` | `deluge` | Seedbox provider: `deluge` or `putio` |
| `DOWNLOAD_DIR` | *required* | Local directory for downloaded files |
| `TARGET_LABEL` | | Label/tag to filter transfers |
| `KEEP_DOWNLOADED_FOR` | `24h` | How long to keep local files before cleanup |
| `POLLING_INTERVAL` | `10m` | How often to poll for new transfers |
| `CLEANUP_INTERVAL` | `10m` | How often to run the cleanup job |
| `MAX_PARALLEL` | `5` | Max concurrent file downloads |
| `LOG_LEVEL` | `INFO` | Log level: `DEBUG`, `INFO`, `WARN`, `ERROR` |
| `DB_PATH` | `downloads.db` | Path to the SQLite database |
| `DISCORD_WEBHOOK_URL` | | Discord webhook for notifications |

## Deluge Settings

| Variable | Description |
|---|---|
| `DELUGE_BASE_URL` | Base URL for the Deluge web UI |
| `DELUGE_API_URL_PATH` | JSON-RPC endpoint path (e.g., `/deluge/json`) |
| `DELUGE_USERNAME` | Deluge web UI username |
| `DELUGE_PASSWORD` | Deluge web UI password |
| `DELUGE_COMPLETED_DIR` | Directory for completed downloads |

## Put.io Settings

| Variable | Default | Description |
|---|---|---|
| `PUTIO_TOKEN` | *required* | Your Put.io OAuth token |
| `PUTIO_BASE_DIR` | *required* | Directory in Put.io for file storage |
| `PUTIO_SEED_RATIO` | `0` | Target seed ratio before cleanup (0 = immediate) |

## Transmission Proxy (for *Arr)

| Variable | Default | Description |
|---|---|---|
| `TRANSMISSION_USERNAME` | *required* | Auth username for the proxy |
| `TRANSMISSION_PASSWORD` | *required* | Auth password for the proxy |
| `WEB_BIND_ADDRESS` | `0.0.0.0:9091` | Proxy listen address |
| `WEB_READ_TIMEOUT` | `30s` | HTTP read timeout |
| `WEB_WRITE_TIMEOUT` | `30s` | HTTP write timeout |
| `WEB_IDLE_TIMEOUT` | `5s` | HTTP idle timeout |
| `WEB_SHUTDOWN_TIMEOUT` | `30s` | Graceful shutdown timeout |

## Web UI

| Variable | Default | Description |
|---|---|---|
| `UI_ENABLED` | `true` | Enable the browser dashboard and its REST API |
| `UI_BIND_ADDRESS` | `0.0.0.0:9092` | Web UI listen address (separate port from the Transmission proxy) |
| `UI_USERNAME` | | Optional basic-auth username — auth is disabled unless both this and `UI_PASSWORD` are set |
| `UI_PASSWORD` | | Optional basic-auth password — auth is disabled unless both this and `UI_USERNAME` are set |

See the [Web UI](web-ui.md) page for details.

## *Arr Integration

| Variable | Description |
|---|---|
| `SONARR_API_KEY` | Sonarr API key for import detection |
| `SONARR_BASE_URL` | Sonarr API URL (e.g., `http://sonarr:8989`) |
| `RADARR_API_KEY` | Radarr API key for import detection |
| `RADARR_BASE_URL` | Radarr API URL (e.g., `http://radarr:7878`) |

## Telemetry

| Variable | Default | Description |
|---|---|---|
| `TELEMETRY_ENABLED` | `true` | Enable OpenTelemetry instrumentation |
| `TELEMETRY_OTEL_ADDRESS` | `0.0.0.0:4317` | OTLP gRPC collector address |
| `TELEMETRY_SERVICE_NAME` | `seedbox_downloader` | Service name in traces/metrics |

# Getting Started

## Docker (recommended)

```sh
docker run --rm -p 9091:9091 -p 9092:9092 \
  -e DOWNLOAD_CLIENT=putio \
  -e PUTIO_TOKEN=your-token \
  -e PUTIO_BASE_DIR=/downloads \
  -e TARGET_LABEL=sonarr \
  -e DOWNLOAD_DIR=/downloads \
  -e TRANSMISSION_USERNAME=admin \
  -e TRANSMISSION_PASSWORD=secret \
  -v /path/to/downloads:/downloads \
  jackcoelho/putioarr:latest
```

Port `9091` serves the Transmission RPC proxy used by *Arr; port `9092` serves the [Web UI](web-ui.md).

## Docker Compose

```yaml
services:
  seedbox_downloader:
    image: jackcoelho/putioarr:latest
    container_name: seedbox_downloader
    environment:
      DOWNLOAD_CLIENT: "putio"
      PUTIO_TOKEN: "your-putio-token"
      PUTIO_BASE_DIR: "/downloads"
      TARGET_LABEL: "sonarr"
      DOWNLOAD_DIR: "/downloads"
      KEEP_DOWNLOADED_FOR: "168h"
      TRANSMISSION_USERNAME: "admin"
      TRANSMISSION_PASSWORD: "secret"
      WEB_BIND_ADDRESS: "0.0.0.0:9091"
      # Web UI dashboard (enabled by default on 9092)
      UI_BIND_ADDRESS: "0.0.0.0:9092"
      # Optional: *Arr integration for import detection
      SONARR_API_KEY: "your-sonarr-api-key"
      SONARR_BASE_URL: "http://sonarr:8989"
      RADARR_API_KEY: "your-radarr-api-key"
      RADARR_BASE_URL: "http://radarr:7878"
      # Optional: notifications
      DISCORD_WEBHOOK_URL: "https://discord.com/api/webhooks/..."
    ports:
      - "9091:9091" # Transmission RPC proxy (for *Arr)
      - "9092:9092" # Web UI dashboard
    volumes:
      - downloads:/downloads
    restart: unless-stopped

volumes:
  downloads:
```

!!! tip
    Use a `.env` file to keep secrets out of your compose file. See the [Docker docs](https://docs.docker.com/compose/environment-variables/) for details.

## Build from source

```sh
git clone https://github.com/fcjack/putioarr.git
cd putioarr
go build -o seedbox_downloader ./cmd/seedbox_downloader
./seedbox_downloader
```

!!! note
    Requires Go 1.26+ and CGO enabled (for SQLite). To build the binary with the Web UI embedded, run `make build-all`.

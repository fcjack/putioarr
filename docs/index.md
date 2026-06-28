# PutioArr

**Automated media pipeline that bridges your seedbox with Sonarr, Radarr, and other *Arr applications.**

[![Build](https://github.com/fcjack/putioarr/actions/workflows/main.yml/badge.svg)](https://github.com/fcjack/putioarr/actions/workflows/main.yml)
![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)
![Go Version](https://img.shields.io/badge/Go-1.26-00ADD8?logo=go&logoColor=white)
![Docker](https://img.shields.io/badge/Docker-jackcoelho%2Fputioarr-2496ED?logo=docker&logoColor=white)

!!! warning "Disclaimer"
    This project is provided for educational and legal use only. The author does **not** incentivize, condone, or support piracy or the illegal downloading, sharing, or distribution of copyrighted material. It is your responsibility to ensure compliance with all applicable laws in your jurisdiction.

## What is this?

Seedbox Downloader is an event-driven Go service that automatically downloads completed torrents from your seedbox and integrates with the *Arr ecosystem. It supports **Deluge** and **Put.io** as seedbox providers, with a built-in **Transmission RPC proxy** so Sonarr and Radarr treat it like a native download client.

## Key Features

- **Dual seedbox support** — Deluge (JSON-RPC) and Put.io (OAuth2 API)
- **Transmission RPC proxy** — *Arr apps see it as a Transmission client, no extra config needed
- **Web UI** — Browser dashboard to monitor, retry, cancel, and remove transfers, plus admin actions
- **Automatic import detection** — Monitors Sonarr/Radarr until files are imported, then cleans up
- **Seed ratio enforcement** — Optionally wait for a target seed ratio before removing transfers
- **Parallel downloads** — Configurable concurrency with progress tracking
- **Discord notifications** — Rich embeds for download events, failures, and missing transfers
- **Full observability** — OpenTelemetry traces, Prometheus metrics, Grafana dashboards included
- **SQLite state tracking** — Atomic transfer claiming prevents duplicate processing
- **Distroless Docker image** — Minimal, secure, non-root container

## How It Works

```text
                    ┌──────────────┐
                    │  Seedbox     │
                    │ (Deluge /    │
                    │  Put.io)     │
                    └──────┬───────┘
                           │ poll for tagged transfers
                           ▼
┌────────────┐    ┌────────────────┐    ┌──────────────┐
│  Sonarr /  │◄──►│   Seedbox      │───►│   SQLite DB  │
│  Radarr    │    │   Downloader   │    │  (state)     │
│  (*Arr)    │    │                │    └──────────────┘
└────────────┘    │  - Download    │
   ▲  Transmission│  - Track       │    ┌──────────────┐
   │  RPC proxy   │  - Import mon. │───►│   Discord     │
   └──────────────│  - Cleanup     │    │  (webhooks)   │
                  └────────┬───────┘    └──────────────┘
                           │
                           ▼
                    ┌──────────────┐
                    │  /downloads  │
                    │  (local fs)  │
                    └──────────────┘
```

**Pipeline flow:**

1. Polls seedbox for torrents matching a label/tag
2. Claims transfers atomically in SQLite (safe for multiple instances)
3. Downloads files in parallel to local storage
4. Monitors *Arr APIs until import is confirmed
5. Waits for seed ratio threshold (if configured)
6. Cleans up transfer from seedbox and local storage
7. Sends Discord notifications at each stage

## Next steps

- [Getting Started](getting-started.md) — run it with Docker or from source
- [Configuration](configuration.md) — every environment variable
- [Put.io + *Arr](integration.md) — wire it into Sonarr/Radarr
- [Web UI](web-ui.md) — the browser dashboard
- [Monitoring](monitoring.md) — Prometheus + Grafana stack

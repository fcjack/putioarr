# Web UI

A built-in browser dashboard gives you visibility and control over the download pipeline — see every transfer's composite status, watch live progress, and recover from failures without touching the database or the filesystem by hand.

It runs as a self-contained single-page app embedded directly in the binary (no extra container or reverse proxy needed) and is served on its **own port**, separate from the Transmission RPC proxy.

## Transfers

The main view lists every transfer with a merged status (Put.io + local download + import state), live progress, size, and speed. Per-row actions adapt to the transfer's state — **Retry** for failed/orphaned items, **Cancel** for in-flight local downloads, and **Delete** for everything.

![Web UI — transfers list](images/web-ui-transfers.png)

Selecting a transfer opens a detail drawer with the full pipeline timeline, file list, save path, and any error message, alongside the same actions.

![Web UI — transfer detail](images/web-ui-detail.png)

## Admin

The admin tab surfaces the running configuration and a guarded "danger zone" for resetting the state database or purging the download directory. Destructive operations require a short-lived confirmation token, so they can't be triggered by accident or CSRF.

![Web UI — admin panel](images/web-ui-admin.png)

## Enabling & accessing

The Web UI is **enabled by default** on port `9092`. Expose the port and open it in your browser:

```sh
docker run --rm -p 9091:9091 -p 9092:9092 \
  -e DOWNLOAD_CLIENT=putio \
  -e PUTIO_TOKEN=your-token \
  -e PUTIO_BASE_DIR=/downloads \
  -e DOWNLOAD_DIR=/downloads \
  -e TARGET_LABEL=sonarr \
  -e TRANSMISSION_USERNAME=admin \
  -e TRANSMISSION_PASSWORD=secret \
  -v /path/to/downloads:/downloads \
  jackcoelho/putioarr:latest
```

Then visit `http://localhost:9092`.

## Authentication

The Web UI is **open by default** — it's a read-mostly helper for following downloads and exposes no secrets, so no login is required out of the box.

To put it behind **HTTP Basic Auth**, set both `UI_USERNAME` and `UI_PASSWORD`; the UI and its REST API (`/api/v1`) will then require those credentials. If either is unset, auth stays disabled.

Regardless of auth, destructive admin actions (DB reset, directory purge, delete) still require a short-lived confirmation token, so they can't be triggered accidentally or via CSRF.

!!! warning
    If you expose the UI beyond a trusted network, set credentials and put it behind a reverse proxy with HTTPS.

## REST API

The dashboard is backed by a JSON API under `/api/v1`, separate from the Transmission RPC endpoint:

```text
GET    /api/v1/transfers              # list with filters
GET    /api/v1/transfers/{id}         # detail + timeline
POST   /api/v1/transfers/{id}/retry
POST   /api/v1/transfers/{id}/cancel
DELETE /api/v1/transfers/{id}         # scopes: putio, local, db
POST   /api/v1/admin/db/reset
POST   /api/v1/admin/downloads/purge
GET    /api/v1/admin/confirm-token
GET    /api/v1/health
GET    /api/v1/config                 # non-secret config snapshot
```

## Disabling

Set `UI_ENABLED=false` to turn the dashboard off entirely; the Transmission RPC proxy is unaffected.

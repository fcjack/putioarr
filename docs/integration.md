# Put.io + *Arr Integration

The Transmission RPC proxy lets Sonarr, Radarr, and other *Arr apps use Put.io as if it were a Transmission download client.

## Setup in *Arr

1. Deploy the service with `DOWNLOAD_CLIENT=putio` and the Transmission proxy credentials
2. In your *Arr app, go to **Settings > Download Clients > Add**
3. Select **Transmission** and configure:

| Setting | Value |
|---|---|
| Host | Your server IP/hostname |
| Port | `9091` |
| URL Base | `/transmission` |
| Username | Your `TRANSMISSION_USERNAME` |
| Password | Your `TRANSMISSION_PASSWORD` |
| Category | An existing folder in your Put.io account |

4. Test the connection and save

## Directory Mapping

| Variable | Where | Purpose |
|---|---|---|
| `PUTIO_BASE_DIR` | Put.io cloud | Where files are stored in Put.io |
| `DOWNLOAD_DIR` | Local filesystem | Where files are downloaded to |

Both paths typically point to `/downloads`. *Arr reads from the local `DOWNLOAD_DIR` after files are downloaded.

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

Configure seeding goals on each torrent indexer in Sonarr/Radarr (for example, ratio `1.0` and seed time `4320` minutes). putioarr stores those per-torrent values from Transmission `torrent-set` requests and reports torrents stopped when either goal is reached; *Arr then removes the transfer through `torrent-remove` when **Remove Completed Downloads** is enabled. Existing torrents without a saved goal use putioarr defaults: ratio `1.0` or `4320` minutes.

## Directory Mapping

| Variable | Where | Purpose |
|---|---|---|
| `PUTIO_BASE_DIR` | Put.io cloud | Where files are stored in Put.io |
| `DOWNLOAD_DIR` | Local filesystem | Where files are downloaded to |

Both paths typically point to `/downloads`. *Arr reads from the local `DOWNLOAD_DIR` after files are downloaded.

# Monitoring

The project ships with a complete Prometheus + Grafana monitoring stack in the `monitoring/` directory.

```sh
docker compose -f docker-compose.telemetry.yml up -d
```

| Service | URL |
|---|---|
| Grafana | `http://localhost:3000` (admin/admin) |
| Prometheus | `http://localhost:9090` |
| Metrics endpoint | `http://localhost:2112/metrics` |

## Included Metrics

- **RED** — HTTP request rate, error rate, response latency (p95)
- **Business** — Downloads by status, active transfers, download duration, client operations
- **USE** — Memory usage, goroutine count, uptime, error rates by component
- **Database** — Operation rates by type, query duration histograms

See [TELEMETRY.md](https://github.com/fcjack/putioarr/blob/main/TELEMETRY.md) for full details on the instrumentation.

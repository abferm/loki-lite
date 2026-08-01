# loki-lite

[![GoDoc](https://pkg.go.dev/badge/github.com/abferm/loki-lite)](https://pkg.go.dev/github.com/abferm/loki-lite)
[![Docker Hub](https://img.shields.io/docker/pulls/abferm/loki-lite?label=Docker%20Hub)](https://hub.docker.com/r/abferm/loki-lite)
[![CI](https://github.com/abferm/loki-lite/actions/workflows/ci.yml/badge.svg)](https://github.com/abferm/loki-lite/actions/workflows/ci.yml)

A Loki-compatible query interface for journald logs. Query-only with no storage of its own — it translates Loki API requests into journald queries.

## Requirements

- **Query only** — no storage, translates Loki API requests into journald queries
- **Pure Go** — installable via `go install github.com/abferm/loki-lite@latest`
- **1-to-1 Loki compatibility** — works with Grafana, logcli, and other Loki clients
- **Embeddable** — other Go apps can import and run the server as an HTTP handler

## Demo

The demo stack (`demo/compose.yaml`) runs loki-lite next to a Grafana instance with the datasource pre-provisioned, ready to explore your host's journald logs:

![loki-lite demo](demo/demo.gif)

```bash
docker compose -f demo/compose.yaml up -d --build
```

Then open http://localhost:3000 (Grafana) and query Loki Lite in Explore, or talk to http://localhost:3100 directly with logcli or curl.

## API documentation

See [API.md](API.md) for the supported Loki API endpoints and request/response details.

## Quick start

```bash
docker compose up -d              # build and start the dev container
docker compose exec dev bash      # open a shell as developer
docker compose down               # stop the container
```

For VS Code: open the repo root and run **Dev Containers: Reopen in Container**.

## Production build

Tests run as part of the build — the production image is only produced if
the test suite passes.

```bash
docker build --target production -t loki-lite .
```

## Run from Docker Hub

Pre-built multi-arch images are published to [Docker Hub](https://hub.docker.com/r/abferm/loki-lite) on every tagged release (`latest`, `<version>`, `<major>`, `<major>.<minor>`).

The container reads your host's journald logs, so it needs read access to `/var/log/journal` and membership in the `systemd-journal` group (GID `101` on most distros — adjust if yours differs).

### docker run

```bash
docker run -d \
  --name loki-lite \
  -p 3100:3100 \
  -v /var/log/journal:/var/log/journal:ro \
  --group-add 101 \
  abferm/loki-lite:latest
```

### docker compose

```yaml
services:
  loki-lite:
    image: abferm/loki-lite:latest
    restart: unless-stopped
    group_add:
      - "101"
    volumes:
      - /var/log/journal:/var/log/journal:ro
    ports:
      - "3100:3100"
```

Then query http://localhost:3100 with logcli or any Loki client.

The image runs with a bundled default config and honours the `JOURNAL_DIR`, `JOURNAL_NAME`, `ADDR`, and `POOL_MAX` environment variables (see [config.toml](config.toml)). To override anything else, mount your own TOML at `/app/config.toml`.

## Testing

Run the test suite in Docker and extract a JUnit XML report (plus a
`.status` marker) into the local `reports/` directory:

```bash
docker build --target test-reports -o type=local,dest=reports .
```

The report is published to CI (see `.github/workflows/ci.yml`) via
`EnricoMi/publish-unit-test-result-action` when tests run on GitHub.

## License

MIT

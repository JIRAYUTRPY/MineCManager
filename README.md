# MineCManager

Go control plane and VM agent for managing a systemd-hosted Minecraft server. The panel includes a Thai admin UI, scoped API keys, node registration, plugin/config versioning, stopped-server backups and verified restore.

## Build and verify

Linux, Go 1.25+, Python 3 (smoke test only):

```bash
go build -o bin/mcm-agent ./cmd/agent
go build -o bin/mcm-panel ./cmd/panel
go test -race ./...
go vet ./...
python3 scripts/smoke-test.py
```

The smoke test starts both binaries with an isolated fake systemctl; it does not operate a live VM. Optional browser verification requires Playwright with Chromium: `MCM_UI_TEST=1 python3 scripts/smoke-test.py`. Set `MCM_CHROMIUM` to a system Chromium executable if needed.

- [Setup and operational recovery](docs/operations.md)
- [Panel authorization, keys, node registration and audit](docs/panel.md)
- [Plugin/config management](docs/plugin-management.md)
- [Backup and restore](docs/backup.md)
- [Architecture and boundaries](docs/architecture.md)
- [Acceptance scope and remaining product work](docs/next-steps.md)
- [OpenAPI](api/openapi.yaml)

This repository manages VM server files and service lifecycle. A Paper runtime bridge for player inventory/item changes and world resource indexing is a separate, unimplemented product layer. No production host is configured by building or testing this repository.

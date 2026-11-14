# MineCManager

VM-based Minecraft server management foundation.

## Current implementation

Go agent prototype with bearer-token authorization, loopback-only HTTP listener, allowlisted systemd actions, read-only service status, and confined directory listing. This is not production ready.

```bash
go test ./...
export MCM_AGENT_TOKEN="$(openssl rand -hex 32)"
export MCM_SERVER_ROOT=/srv/minecraft
export MCM_SYSTEMD_UNIT=minecraft.service
go run ./cmd/agent
```

Example (run on the VM):

```bash
curl -H "Authorization: Bearer $MCM_AGENT_TOKEN" http://127.0.0.1:9081/v1/status
curl -X POST -H "Authorization: Bearer $MCM_AGENT_TOKEN" -H 'Content-Type: application/json' -d '{"action":"restart"}' http://127.0.0.1:9081/v1/actions
```

See [architecture and security limitations](docs/architecture.md). Do not expose the agent directly to the public internet. No deployment is performed by this repository.

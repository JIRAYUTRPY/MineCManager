# MineCManager foundation

## Trust boundaries

Panel/API is the future central control plane. Agent runs on each VM and is not exposed to the internet. Current agent binds **127.0.0.1:9081 only**. Use an authenticated private tunnel (e.g. WireGuard) or reverse proxy with mTLS to connect the Panel to agents; do not publicly publish port 9081.

The initial agent supports read-only status, allowlisted systemd start/stop/restart and directory listing constrained to a configured Minecraft root. The systemd unit name is fixed at startup. No arbitrary shell execution endpoint is provided.

## Required configuration

- `MCM_AGENT_TOKEN`: random secret of at least 32 characters, provisioned through a protected environment file
- `MCM_SERVER_ROOT`: absolute directory containing Minecraft server data
- `MCM_SYSTEMD_UNIT`: dedicated service name such as `minecraft.service`

The agent process needs minimal filesystem permissions and narrowly scoped systemd privileges. Do not run as root by default. Requests use `Authorization: Bearer <token>`.

## Remaining work

This is an initial **agent-only** foundation, not a complete management product. Panel/API, RBAC, per-key scopes, key rotation, centralized audit, OpenAPI, plugin edit/install/rollback, backup/restore, metrics, full file management, TLS transport, frontend, and deployment scripts are not implemented. These require dedicated design and tests before production deployment.

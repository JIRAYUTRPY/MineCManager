# Panel API prototype

The panel listens on `127.0.0.1:9080`. It provides a minimal control-plane API, not a public endpoint or a multi-VM network implementation. Node URLs are deliberately restricted to loopback IP addresses; remote nodes require a separate authenticated tunnel or socket forwarding solution and further security review.

## Configuration

Generate a random API key and store only its SHA-256 digest in the config:

```bash
KEY="$(openssl rand -hex 32)"
printf '%s' "$KEY" | sha256sum
```

Copy `config/panel.example.json` to a private location, replace the placeholder digest, and set `MCM_PANEL_CONFIG` to its absolute path. Set `MCM_NODE_01_TOKEN` to the same bearer token as the agent. Set `MCM_AUDIT_PATH` to a protected, append-only operational log location. Keep the original key in a secrets manager, not in Git.

```bash
MCM_PANEL_CONFIG=/etc/mcm/panel.json MCM_AUDIT_PATH=/var/log/mcm/audit.jsonl go run ./cmd/panel
```

## Routes

- `GET /v1/nodes` requires `nodes:read`
- `GET /v1/nodes/{id}/status` requires `nodes:read`
- `POST /v1/nodes/{id}/actions` with JSON `{"action":"restart"}` requires `nodes:control`

Use `Authorization: Bearer <API_KEY>`. Audit records include key ID, node ID, action, result status, and timestamp. No raw tokens are logged.

## Security limitations

This is a **prototype**. Keys are configured manually and cannot be created or revoked via API yet. No database, tenant isolation, persistent node enrollment, TLS, UI, or central role management. Audit writes are best-effort and not tamper-evident. The HTTP client is configured to reject redirects, but network and transport policy must be hardened before remote-node support. Do not expose panel or agent ports publicly.

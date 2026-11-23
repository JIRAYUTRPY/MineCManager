# Panel API and key lifecycle

Start with `config/panel.example.json`. Generate a 32-byte random key, store its SHA-256 digest in the bootstrap key entry, and retain the original only in your secret manager. `MCM_PANEL_CONFIG` points to the writable config file; its parent must be writable for atomic replacement. `MCM_AUDIT_PATH` points to an append-only-by-convention JSONL log. Both files and their parent directory must be private to the panel user.

Open the Panel through a loopback SSH tunnel or HTTPS reverse proxy. On the first visit, use the bootstrap key at `/setup` to create the sole administrator. Subsequent visits use a username and password at `/login`. The administrator creates accounts and roles at `/admin/users` and `/admin/roles`. Pages and API operations enforce the same scopes. Account data, sessions and per-user page preferences live in SQLite next to the Panel configuration. [Thai account guide](users-rbac-th.md)

## Scopes

| Scope | Operations |
|---|---|
| nodes:read | Visible node list, live status, files listing, metrics |
| nodes:control | Fixed-unit start, stop, restart |
| plugins:read / plugins:write | Inventory/history / install, replace, restore, delete |
| configs:read / configs:write | Config content/history / edit and rollback |
| backups:read / backups:write / backups:restore | Discovery/list / create / verified restore |
| players:read / inventory:read / inventory:write / resources:read | Paper online players / inventory inspection / guarded slot replacement / live aggregates |
| admin:fleet | Fleet VM and cloud administration |
| users:manage | Human account and role administration in the browser |
| admin:keys | List key metadata, issue, revoke |
| admin:nodes | Register and remove nodes |
| audit:read | Paginated local audit log |

Keys can have `nodes: ["minecraft-01"]`; omit/empty means all nodes. Administrative keys cannot be node restricted. `audit:read` is a global privilege; its results are not node-filtered. Expiration is an optional RFC3339 UTC timestamp. Revocation takes effect for subsequent authorization decisions; requests already authorized may finish even after revocation, but revocation does not wait for slow agent requests and subsequent authorization is denied. Revocation of the last active key administrator is rejected.

- `POST /v1/keys`: `{ "id":"reader", "scopes":["nodes:read"], "nodes":["minecraft-01"], "expires_at":"2027-01-01T00:00:00Z" }`. Returns a generated token once. Optional `token` accepts your own secret (32–1024 characters); use high entropy. Only its digest is persisted.
- `GET /v1/keys`: metadata only, no tokens or hashes.
- `DELETE /v1/keys/{id}`: revoke, preserving the key record for audit identity. Rotate by creating a replacement, switching callers, then revoking the old key. IDs/hashes cannot be reused.
- `POST /v1/nodes`: `{ "id":"minecraft-02", "url":"https://agent.internal:9081", "token_env":"MCM_NODE_02_TOKEN" }`. Token must already exist in the panel process environment and contain at least 32 characters. URLs allow loopback HTTP or verified HTTPS. Redirects and environment HTTP proxies are disabled. Provision secrets/certificates outside the API.
- `DELETE /v1/registrations/{nodeId}`: remove routing registration, without touching the VM. Re-register to change URL/token reference. Review existing node-restricted keys before reusing an ID.
- `GET /v1/audit?offset=0&limit=100`: records and `next_offset`; maximum 1,000 records. Offset is a line index and assumes the file is not truncated/rotated during pagination.

Configuration uses JSON to keep this VM foundation self-contained. PostgreSQL, multi-process replication, federation/SSO and automatic secret distribution are not implemented. Stop the panel before externally editing config or rotating its audit file; restart to reopen/reload. Audit does not store request bodies, key secrets or agent tokens. Intent/result records cover dispatched node operations and state-changing admin operations; rejected requests may lack records when the audit device itself is unavailable.

## Audit failure recovery

A write/fsync failure latches the running panel into an audit-failed state. New privileged operations are rejected until the panel restarts; freeing disk space alone does not silently resume dispatch. At startup the panel checks that the existing log consists of JSON object records, ends in a newline and respects the reader size limit. Invalid or partial records prevent startup without altering the original file.

Stop the panel, preserve a copy of the damaged log, correct the disk/permission issue, and reconcile incomplete intent/result pairs against node state. Recover a validated log or archive the damaged file and deliberately start a new protected log according to your audit policy; retain the archive and reconciliation record. Do not blindly truncate a partial record or retry an uncertain mutation. Startup validation scans the log, so rotate large files while the panel is stopped as described above.

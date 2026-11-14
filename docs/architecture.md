# Architecture and trust boundaries

Browser -> loopback Panel -> local HTTP or remote mTLS Agent -> one fixed systemd unit and one server root.

The browser holds a panel key in memory; it does not receive agent credentials. The panel hashes incoming keys, enforces operation scopes and optional node restrictions, and exchanges credentials when forwarding to an agent. Node secrets are referenced by environment variable name, never returned by APIs. Agent identity is established by the HTTPS server certificate; remote callers must also present a client certificate trusted by the agent CA. The agent still requires its independent bearer token.

Panel state is a single-process, atomic JSON configuration (0600), protected by a process lock. API mutations persist before publishing new in-memory state. The audit JSONL file is a separate durable local log, not a database transaction or a tamper-proof ledger. Each dispatched request records and fsyncs an intent; successful responses require an fsynced result. A timeout, process crash or failed result write can leave an unknown outcome. Do not automatically retry mutations; inspect current state and audit by request ID first. Local privileged operators can modify these files; off-host log shipping is an operational requirement if tamper resistance is needed.

The agent serializes service actions, plugin/config mutations and backup/restore operations. File writes use rooted filesystem operations, staging, fsync and atomic publish; existing files require a matching SHA-256. It never accepts arbitrary shell commands or a client-selected systemd unit. The state directory holds version blobs and complete backups outside the live root and must be private to the agent. Only one agent process may use that state directory.

## Trust assumptions

- Admin keys can register HTTPS destinations and issue other admin keys; grant these scopes only to infrastructure administrators. There is no tenant boundary or key-delegation hierarchy.
- Server files/plugins are executable trusted server content. JAR structural validation is not a malware/signature check. A plugin with OS execution capabilities can modify any file its Minecraft OS identity can access.
- Minecraft and the agent must use distinct OS users, with shared access only to the server tree; panel/agent secrets and agent state must not be readable by the Minecraft account.
- Do not use a second control mechanism to start Minecraft or mutate server files during an agent operation. `inactive` proves the configured unit is stopped, not that unrelated external processes have stopped writing.
- The server root, its parent and the agent state path must be provisioned by trusted administrators. Permissions and Linux service sandboxing remain deployment responsibilities.

Panel binds 127.0.0.1:9080. Serve it through an HTTPS reverse proxy/private tunnel. It has no CORS allowance or cookie authentication. Remote agent listeners refuse to start without mTLS; no `InsecureSkipVerify` path exists. Host metrics are polled on demand, not a historical monitoring database.

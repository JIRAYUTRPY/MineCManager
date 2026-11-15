# Build, provision and recover

For copyable step-by-step setup, use the [Thai installation guide](install-th.md).

These instructions/templates are for review and later deployment. They have not been run against a real host. Use Linux/systemd, Go 1.25+ to build, and a Minecraft Java runtime appropriate to your selected server version. No Java/Paper version is selected or downloaded by this agent.

## Filesystem and OS identities

Provision separate `mcm-agent`, `mcm-panel`, and Minecraft service users. Use a shared `minecraft` group for game files only. Example layout:

| Path | Owner / permissions | Purpose |
|---|---|---|
| /srv/minecraft-host | administrator: minecraft, 0770 | Parent allows agent restore renames; grant only trusted identities |
| /srv/minecraft-host/server | Minecraft user: minecraft, 0770 | Existing server root; group read/write required for agent |
| /var/lib/mcm-agent | mcm-agent: minecraft, 0700 | Private backup/version state and process lock |
| /var/lib/mcm-panel | mcm-panel: mcm-panel, 0700 | panel.json, audit.jsonl, process lock |
| /etc/mcm/agent.env | root: root, 0600 | systemd loads agent secrets |
| /etc/mcm/panel.env | root: root, 0600 | systemd loads panel secrets |
| /etc/mcm/tls | respective service user only | Certificates and private keys |

Agent needs group write access throughout the live root and parent. Restored files belong to mcm-agent:minecraft with mode 0660 and nested directories use 0770, allowing the Minecraft service to continue writing through the shared group. The restored root keeps its previous permissions; provision that root as 0770. The agent must use minecraft as its primary group. Do not grant Minecraft access to the private state or secrets directories.

Build binaries using README commands, then install them at the template paths. Existing Minecraft must run as a fixed systemd unit (example `minecraft.service`) and use `KillMode=control-group` so stopping waits for all child processes. Its `WorkingDirectory` must be the configured server root. Server EULA acceptance, Java install and world provisioning remain explicit operator actions.

## Agent environment

```dotenv
MCM_AGENT_TOKEN=REPLACE_WITH_RANDOM_SECRET_AT_LEAST_32_CHARS
MCM_SERVER_ROOT=/srv/minecraft-host/server
MCM_SYSTEMD_UNIT=minecraft.service
MCM_STATE_ROOT=/var/lib/mcm-agent
```

Defaults to loopback 127.0.0.1:9081. For remote access, also configure:

```dotenv
MCM_AGENT_ADDR=0.0.0.0:9081
MCM_SERVER_CERT=/etc/mcm/tls/agent.crt
MCM_SERVER_KEY=/etc/mcm/tls/agent.key
MCM_CA_CERT=/etc/mcm/tls/client-ca.crt
```

Use a server certificate whose SAN matches the registered URL and a client certificate with client-auth EKU. Firewall the agent to panel addresses/private networks. A dedicated CA per environment is preferable. Certificate/token changes require controlled service restarts; rotation is provision-new/switch/revoke, not an unauthenticated enrollment protocol.

## Panel environment

```dotenv
MCM_PANEL_CONFIG=/var/lib/mcm-panel/panel.json
MCM_AUDIT_PATH=/var/lib/mcm-panel/audit.jsonl
MCM_NODE_01_TOKEN=REPLACE_WITH_THE_AGENT_TOKEN
MCM_CLIENT_CERT=/etc/mcm/tls/panel.crt
MCM_CLIENT_KEY=/etc/mcm/tls/panel.key
MCM_CA_CERT=/etc/mcm/tls/server-ca.crt
```

The three TLS settings are optional for local loopback agents. Generate a bootstrap API key with `openssl rand -hex 32`, hash its exact bytes using `printf '%s' "$KEY" | sha256sum`, and replace the example digest in panel.json. Keep the original key private. The API can issue additional restricted keys and revoke old ones after startup. The panel loads environment variables at process startup, so add node credentials before registering their reference.

Review `deploy/*.service` paths and `deploy/49-mcm-agent.rules`. The Polkit rule permits only start/stop/restart of the fixed Minecraft unit. It does not grant sudo or arbitrary command execution. Install through your normal host change process after validating on a disposable VM. Panel listens on loopback; publish only through your HTTPS/private access layer. No nginx/DNS/firewall changes are made by this repository.

## Acceptance on the real host

1. Verify the agent can read/write game files but cannot read panel secrets; Minecraft cannot read agent state or either service's secrets.
2. Check allowed systemctl operations as mcm-agent; verify another unit and arbitrary operations are denied.
3. Connect through mTLS; verify missing/incorrect client certificates fail. Confirm node token authorization separately.
4. Use a read-only panel key; check mutation denial and node restriction.
5. Stop Minecraft, install a trusted test JAR, edit a config and roll both back.
6. Create a snapshot; modify test world data; restore; verify previous root is retained and ownership/permissions let Minecraft resume.
7. Start the server, join with a test player and confirm world/plugin behavior. Retain previous root until this passes.
8. Test audit disk failure and examine incomplete intent/result pairs after an interrupted operation. Inspect state before retrying.

See [backup recovery](backup.md) for power-loss windows and disk/permission requirements. Do not use multiple panel/agent instances against the same local state. Process locks prevent accidental duplicate daemons, not distributed access across hosts.

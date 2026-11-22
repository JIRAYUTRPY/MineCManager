# Fleet control plane (v0.3)

The panel-only Compose deployment serves the Thai UI at `/fleet`. Existing `/` and `/v1/nodes` retain their scoped Agent/Paper behavior. Fleet does not auto-register its servers with that API: it is an additional privileged host control plane.

## Trust and execution

- A dedicated `admin:fleet` scope grants access to all enrolled hosts, OS operations and cloud credentials. Node-restricted administrative keys are rejected by existing config validation. This is intentionally a root-administrator trust boundary, not a multi-tenant sandbox.
- Host enrollment accepts private/public inventory addresses and a distinct reachable SSH IP, password or private key, SSH port, username and independently confirmed SHA256 host fingerprint. Discovery aborts the handshake before authentication. Each execution verifies the pinned fingerprint. A test-only request reads CPU, RAM, disk and uptime through sudo. Create and replace requests run the same probe before committing credentials and recheck authorization and the observed registration before saving. Changing the SSH endpoint or fingerprint waits for pending jobs to finish (409).
- Credentials, job bodies/results, schedules and Cloudflare configuration live in one AES-256-GCM authenticated vault. The random nonce changes for each atomic write. The separate 32-byte key is required to recover state. Secret fields are omitted from reads. File permissions and backup encryption remain part of key protection.
- A single durable worker claims jobs before contacting a VM. Authorization is checked again by actor ID at dispatch. Audit intent must sync before execution. Result audit failure means an unknown outcome, not safe-to-retry. Interrupted work is never automatically replayed; queued work can continue after restart.
- VM commands carry the fixed, embedded Python program over SSH. JSON input is framed on stdin; user values are not interpolated into shell command text. A password may be consumed by sudo, and the frame parser also handles passwordless sudo. SSH key authentication requires passwordless sudo unless a password is also supplied.
- VM operations require root/sudo. The metrics probe reads kernel counters without creating a state directory or taking the VM lock. Other operations use `/var/lib/minecmanager/control.lock`. Jobs still share the Panel worker. SSH has network/handshake timeouts, jobs have a 45-minute context, OS commands have a 120-second limit, remote stdout and editor sizes are bounded.
- OS command execution is deliberately arbitrary root Bash after explicit confirmation. It is not appropriate for untrusted plugin admins. The terminal is non-interactive; no PTY/WebSocket terminal, automatic firewall safety rollback, or package-management transaction UI is implemented.

## Paths and platform behavior

| Path | Purpose |
|---|---|
| Panel `/data/panel.json` | Existing API key digests and Agent registrations |
| Panel `/data/fleet.vault` | Encrypted host/cloud/job/schedule state |
| Panel `/data/vault.key` | Raw 32-byte AES key, never regenerate for an existing vault |
| Panel `/data/admin-key.txt` | Initial administrative credential; protect/rotate after setup |
| Panel `/data/audit.jsonl` | Existing fsynced intent/result journal |
| VM `/srv/minecmanager/<id>` | Game files, write access restricted by service sandbox |
| VM `/etc/systemd/system/mcm-<id>.service` | Generated service |
| VM `/var/lib/minecmanager/servers/*.json` | Build, port, RAM and path metadata |
| VM `/var/lib/minecmanager/backups/<id>` | Archives and SHA256 sidecars |
| VM `/var/lib/minecmanager/file-history` | Original versions of edited OS files |

Minecraft services currently share the `mcm-game` Linux user; systemd restricts writes to each server root but this is not tenant-level confidentiality isolation. Deploy refuses existing paths/units; a failed install retains partial artifacts. Memory reservation uses managed metadata, not a guarantee against other workloads or JVM native-memory pressure. Java major comes from Mojang release metadata; only 17/21/25 are supported. Upstream availability and apt packages determine whether a requested version can deploy.

## Cloudflare and backups

Cloudflare calls use fixed official HTTPS hosts; credentials are not placed in browser responses. DNS supports A/AAAA/CNAME/SRV creates and explicit record-ID updates. Game records default to DNS only. Web proxy opt-in does not provision origin TLS, Tunnel, Spectrum or firewall rules. Zone listing is limited to the first 50 zones.

R2 uses the AWS SDK S3 client, path-style account endpoint and region `auto`. The worker sends a one-hour presigned PUT URL to the VM, not the account secret. The VM streams its stopped-server archive directly to R2. Single PUT has no multipart/resume. Local backups remain if upload fails. R2 retention is configured with bucket lifecycle, independently from the local count retention. R2 list returns up to 1,000 objects; paginated browsing is not implemented.

Backup briefly stops/restarts a running game; no live save-off/save-all integration is assumed. Symlinks/special files are refused. Backup and restore require a loaded unit in a stable active or inactive state. Restore checks again before replacing the root. Local restore verifies SHA256, uses safe tar extraction and stages a new root while preserving the old one. R2 restore downloads to the VM, verifies the operator-provided original backup SHA256, then uses that flow. Restore remains stopped and has a two-rename crash window. File history, previous roots, audit and vault backups require operator retention/monitoring.

## Validation and remaining acceptance

Go tests cover authenticated vault persistence, redaction, revocation, audit failure, job restart semantics, schedule dispatch, host validation, S3 signing, DNS defaults, real SSH protocol host-key verification, input framing and initialization. Python tests exercise filesystem edits, stale writes, backup/restore checksums, server restart on failure, deployment validation and bounded command execution in temporary directories. Browser CI covers UI operations with a mocked runner and enrolls a loopback OpenSSH host from Chromium on a disposable Ubuntu 24.04 runner. The latter uses the Panel binary, a generated SSH key and the running system manager. It checks rejected credentials, fingerprint mismatch, real metrics, backup of an active fixture service, its restart, verified restore and preservation of the previous root.

The Ubuntu test service runs a small process and contains a sample world file. Gameplay, DNS changes and transfers to a live R2 bucket require acceptance with the selected game build and provider account.

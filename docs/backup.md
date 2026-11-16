# Complete stopped-server snapshots

`POST /v1/nodes/{nodeId}/backups` creates a snapshot only while the configured Minecraft unit reports `inactive`. It copies the **whole server root**, so custom world names, datapacks, plugins, configs, player data and server JARs are included. Symlinks/special files cause failure rather than silent omission. Limits: 10 GiB total, 100,000 regular files and a 16 MiB manifest. Empty directories are copied; per-file SHA-256/size/path and snapshot ID/time are recorded.

Snapshots stage under `MCM_STATE_ROOT/backups/.pending-*`, fsync their files/directories, and publish under a random ID. `GET .../backups` lists published IDs. Incomplete hidden staging directories are not listed and may be removed manually after verifying no agent operation is running. Local snapshots are not off-host disaster recovery: copy the complete state directory to independently protected storage and verify that copy separately.

`POST .../backups/{id}/restore` requires `{ "confirm":"<same ID>" }` and `backups:restore`. It checks that the service is inactive, copies the snapshot into a sibling staging directory, and verifies every file against the manifest (including exact file count). Only after verification does it rename the old root to a preserved `...-previous` directory and publish the staged root. The response names `previous_root`. It never restarts Minecraft automatically. Source corruption leaves the live root unchanged.

## Disk, permissions and recovery

- State must exist outside the server root and be private to the agent OS user.
- Restore needs write permission on the server root's **parent**, and space for a complete staging root in addition to the live root and backups. State and live root may be on different filesystems; staging and live root must share the parent filesystem.
- Files are restored as 0660 and nested directories as 0770 under the agent's user/group; the root keeps its prior permission bits. Ownership, ACLs, xattrs, hard-link relationships, executable bits and sparse allocation are not preserved. Use a shared Minecraft group and test access before starting. This backs up game data, not an OS image or `/etc`/systemd/TLS/key configuration.
- Restoring uses two directory renames, not a filesystem-wide atomic transaction. A power loss between them can leave the configured root absent and the complete old root under `.mcm-restore-*-previous`. Keep Minecraft stopped, inspect the staged/preserved roots, and rename the chosen complete directory back. The agent refuses startup when its configured root is missing. Never delete the previous root before a successful game smoke test.
- Backup/restore requests can outlive a disconnected caller. There is no automatic retry or durable job scheduler; an interrupted request has an unknown outcome until files/status are inspected. Keep large requests within configured proxy timeouts (Panel/Agent allow roughly 10 minutes).
- Retention/deletion and off-host replication are operator-managed in this version. Monitor disk space with `/metrics` and remove old snapshots/previous roots only after confirming recovery requirements.

The older `GET /backup/manifest` is a read-only selected-path discovery endpoint. It is **not a backup**, can skip files and is inconsistent on a running server. Use `/backups` for recoverable snapshots.

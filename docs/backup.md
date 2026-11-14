# Backup discovery (read-only)

`GET /v1/backup/manifest` on the Agent returns a SHA-256 manifest of known Minecraft files under the configured server root.

Included when present: server.properties, Bukkit/Spigot/Paper configuration, ops/whitelist/ban lists, plugins, config, and world/world_nether/world_the_end directories. Symlinks and non-regular files are skipped. Files larger than 1 GiB are skipped; at most 10,000 files are hashed. Missing paths are ignored.

This endpoint **does not create a backup**, archive, snapshot, or restore. The scan is not consistent while Minecraft is running. Files may change during hashing. It may be expensive on large worlds. Production backup requires save-off/save-all coordination, snapshot/atomic copy, object storage, encryption, integrity verification, retention, and tested restores. Never mistake the manifest for a recoverable backup.

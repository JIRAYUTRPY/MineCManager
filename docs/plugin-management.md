# Plugin and configuration operations

All agent routes use an agent bearer token. The panel exposes the same suffix under `/v1/nodes/{nodeId}` with separate scopes. Stop the Minecraft unit before install/edit/delete/rollback; the agent checks for exact `inactive` state and serializes these operations against service starts. Restart explicitly after checking the change. There is no hot reload or automatic restart.

## Plugins

- `GET /plugins`: regular `.jar` files with size and SHA-256.
- `GET /plugins/{filename}`: `{plugin, versions}`. A deleted plugin with saved history returns `plugin: null`.
- `PUT /plugins/{filename}`: `Content-Type: application/java-archive`, maximum 16 MiB. New files publish without overwriting another file. Existing files require `If-Match: "<current SHA-256>"`.
- `POST /plugins/{filename}` with `{ "version":"<saved SHA-256>" }`: rollback. Supply current `If-Match`; for a deleted target use `If-None-Match: *`.
- `DELETE /plugins/{filename}`: require matching `If-Match`, save current bytes, then remove the active JAR.

JAR checks include valid ZIP entries/CRC, no traversal/symlinks/duplicate entries, bounded expanded size (256 MiB), entry count (10,000), and a root `plugin.yml` or `paper-plugin.yml` containing name, version and main. This checks structure, not compatibility, bytecode correctness, signatures or safety. Only install trusted plugins.

Versions are SHA-256 blobs under `MCM_STATE_ROOT/versions/<hash-of-relative-path>/<content-hash>`. Restore verifies the blob checksum. Retention is manual: do not delete blobs required by an intended rollback. Config and plugin histories are independent of full server backups.

## Configuration

`GET /config?path=plugins/Example/config.yml` returns UTF-8 content, SHA-256 and version hashes. `PUT` uses the raw text body, a matching quoted `If-Match`, and a 1 MiB limit. `POST` uses `{ "version":"..." }` to roll back. Reads return an ETag; successful writes return the new hash. A stale/missing precondition returns 412 without writing.

Only existing `server.properties`, `bukkit.yml`, `spigot.yml`, or `.yml/.yaml/.json/.properties` files beneath `plugins/` and `config/` are editable. Absolute paths, traversal and symlinks are rejected. JSON and single-document YAML syntax are validated; properties files are checked for UTF-8/NUL only. Plugin-specific config semantics are not validated. Binary JAR modifications require replacing the artifact, not editing Java bytecode through this API.

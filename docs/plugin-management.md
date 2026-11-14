# Plugin management — initial safe subset

Agent endpoints (Bearer agent token, loopback only):
- `GET /v1/plugins` lists regular `.jar` files and SHA-256 checksums.
- `GET /v1/plugins/{filename}` returns plugin metadata.
- `PUT /v1/plugins/{filename}` creates a **new** file (Content-Type `application/java-archive`, maximum 16 MiB).

The upload path is restricted to the `plugins` directory under the configured Minecraft root. The directory must already exist and must not be a symlink. Uploads are staged and atomically published without overwriting an existing filename.

**Important:** These are agent-local operations, not yet proxied by the Panel. Upload is not a safe plugin installation guarantee: files are not verified as JAR/ZIP archives, no signature verification or malware scan exists, and Paper may require a restart. Do not upload untrusted plugins. Versioned replacement, delete, restore, plugin.yml validation, plugin configuration editor, downtime coordination, and central audit are not implemented. The agent must run under a dedicated unprivileged account.

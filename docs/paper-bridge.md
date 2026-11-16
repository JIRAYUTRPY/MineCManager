# Paper runtime bridge

The optional `paper-bridge/` module targets **Paper 1.21.11 / Java 21**, pinned to Paper API build `1.21.11-R0.1-20260511.115010-91`. This is a chosen compatibility baseline, not a claim that it is the newest Minecraft version. It uses public Bukkit/Paper APIs and the standard main-thread scheduler; Folia is not supported.

Build with `mvn -B -ntp -f paper-bridge/pom.xml verify`, or obtain the `mcm-paper-bridge` artifact from the PR CI. Install the resulting JAR through the stopped-server plugin upload workflow. Set the same independently generated `MCM_PAPER_TOKEN` (at least 32 characters) in the Minecraft and Agent systemd environment files; do not reuse the agent or panel token. Restart both services to load environment changes, then start Minecraft. The plugin refuses to enable without its token and listens only on **127.0.0.1:9082**. Do not publish this port.

## Panel routes

| Route | Scope | Behavior |
|---|---|---|
| GET /v1/nodes/{id}/paper/players | players:read | Online UUID/name list |
| GET /v1/nodes/{id}/paper/players/{uuid}/inventory | inventory:read | Slots, item material/count/name, per-slot SHA-256 |
| PUT /v1/nodes/{id}/paper/players/{uuid}/inventory/{slot} | inventory:write | Compare current item hash, then replace the selected slot |
| GET /v1/nodes/{id}/paper/resources | resources:read | Bounded live aggregates of loaded ground item entities and online player inventories |

Inventory writes accept `{ "material":"DIAMOND", "amount":1, "display_name":"Event reward", "lore":["Special edition"] }` and `If-Match: "<current slot hash>"`. Slot is 0–40, amount must be an integer within the material's legal stack size, display name is at most 128 characters, and lore is at most 16 lines of 256 characters. `amount: 0` clears a slot (use `AIR` or an item material). Stale/missing hashes return 412. Reads include all current metadata in the comparison hash, even metadata not represented in the abbreviated UI. A replacement creates a **new** item and does not preserve unlisted enchantments/PDC/custom metadata from the old item.

All Bukkit reads/writes are dispatched on the main server thread. HTTP has a bounded worker pool; agent body size is 16 KiB and bridge scheduling waits at most three seconds. A timeout can still have an unknown outcome if a mutation already started, so reread the slot before retrying. The panel records the player UUID/slot operation under the calling key and request ID; no arbitrary Minecraft commands or operator elevation endpoint exists.

These are **administrative** inventory permissions, not a player self-service login system. Node-restricted keys still have access to all online players on that node when granted inventory scope. Do not issue these scopes to ordinary players expecting UUID ownership enforcement.

Resource responses explicitly identify their scope. They exclude offline inventories, unloaded chunks and container/block inventories, and set `truncated` after 10,000 ground entities or 1,000 players. Aggregation is on demand and can add tick cost; avoid frequent polling on large worlds. A durable complete-world index and historical MongoDB analytics remain separate work.

CI compiles the plugin and tests request validation. Go tests verify proxy routing/credentials and scope separation. Real Paper startup, online player movement/concurrent inventory changes and world performance require a disposable game server acceptance run before production.

API references used: [Paper project setup](https://docs.papermc.io/paper/dev/project-setup/), [scheduler](https://docs.papermc.io/paper/dev/scheduler/), [PlayerInventory](https://jd.papermc.io/paper/1.21.11/org/bukkit/inventory/PlayerInventory.html).

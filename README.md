# MineCManager — Panel-first VM management

[ติดตั้งด้วยสคริปต์เดียวบน Panel VM และ game VM พร้อมปุ่มอัปเดตจาก GitHub Releases](docs/releases-th.md)

[ตั้งบัญชีผู้ดูแลครั้งแรก สร้างผู้ใช้ และกำหนดสิทธิ์](docs/users-rbac-th.md)

เริ่มต้นใหม่: [ติดตั้ง Panel เดียวและเพิ่ม VM ผ่านเว็บ](docs/install-panel-th.md). ใช้ `docker compose up -d --build` แล้วเปิด `/fleet` เพื่อเพิ่ม VM ผ่าน SSH, ดูทรัพยากร, deploy Paper/Purpur/Fabric/Vanilla, จัดการ OS, Cloudflare DNS และ scheduled R2 backups.

Fleet ใช้สิทธิ์ `admin:fleet` ระดับ root แยกจาก Agent API เดิม อ่าน [architecture](docs/fleet-architecture.md) และ [API](api/fleet-openapi.yaml) ก่อนเชื่อม integration. ไม่ต้องติดตั้ง Agent สำหรับ flow ใหม่; จัดการ Agent/Paper ผ่านเมนู Minecraft ที่ `/nodes`.

---

# MineCManager

Go control plane and VM agent for managing a systemd-hosted Minecraft server. The panel includes a Thai admin UI, scoped API keys, node registration, plugin/config versioning, stopped-server backups and verified restore.

## Installation

[คู่มือติดตั้งภาษาไทย — Ubuntu, systemd, Nginx/HTTPS และ mTLS](docs/install-th.md)

## Build and verify

Linux, Go 1.25+, Python 3 (smoke test only):

```bash
go build -o bin/mcm-agent ./cmd/agent
go build -o bin/mcm-panel ./cmd/panel
go test -race ./...
go vet ./...
python3 scripts/smoke-test.py
```

The smoke test starts both binaries with an isolated fake systemctl; it does not operate a live VM. Optional browser verification requires Playwright with Chromium: `MCM_UI_TEST=1 python3 scripts/smoke-test.py`. Set `MCM_CHROMIUM` to a system Chromium executable if needed.

- [Setup and operational recovery](docs/operations.md)
- [Panel authorization, keys, node registration and audit](docs/panel.md)
- [Plugin/config management](docs/plugin-management.md)
- [Backup and restore](docs/backup.md)
- [Architecture and boundaries](docs/architecture.md)
- [Acceptance scope and remaining product work](docs/next-steps.md)
- [Paper runtime bridge](docs/paper-bridge.md)
- [OpenAPI](api/openapi.yaml)

This repository manages VM server files and service lifecycle. An optional Paper bridge adds online player inventory inspection, controlled item replacement and bounded live resource aggregates; full persistent world indexing remains separate. No production host is configured by building or testing this repository.

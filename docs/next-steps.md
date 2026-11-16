# Acceptance scope

This PR completes the implemented **VM management workflow** within the constraints below. It does not implement every feature of the wider MineCManager product.

| VM workflow | Implementation / validation |
|---|---|
| Fixed systemd service control | Agent, panel proxy, scoped authorization; real-binary smoke uses fake systemctl |
| Node registration and keys | Atomic persisted JSON, generated/custom keys, expiry, revocation, node restrictions; lifecycle tests |
| Plugin management | Inventory, structural JAR validation, install/replace/delete, version rollback; conflict/history tests |
| Config editing | Existing allowed paths, syntax validation, optimistic concurrency, rollback; invalid-input tests |
| Backup and restore | Complete stopped-root copy, manifest verification, previous-root retention; corruption/custom-world tests |
| Remote transport | TLS 1.3 mTLS agent; trusted-CA client and missing-client-certificate tests |
| Audit | Fsynced intent before dispatch, result before success; audit failure blocks dispatch tests |
| Admin interface | Thai key-based UI connected to API; optional real-binary Chromium smoke |
| Paper integration | Optional online player inventory/item API and bounded resource aggregates; Java validation tests and scoped Go proxy tests |
| API contract | OpenAPI route/scope/request/response reference |
| Operations | Build/run instructions and service templates; no live VM deployment |

## Separate product scope still open

- Offline player inventory, player self-service identity binding, full persistent world/container indexing and event automation.
- MongoDB/PostgreSQL analytics, user accounts/SSO, tenant boundaries and a horizontally scalable control plane.
- Off-host encrypted backup replication/automated retention, durable async jobs and transactional crash recovery.
- Historical heartbeat/monitoring, centralized immutable audit storage and alerting.
- Validation on a real Ubuntu/systemd/Paper host, reverse proxy and organization-specific firewall/PKI. The local automated smoke does not prove these deployment integrations.

These are explicit remaining product/deployment tasks, not features silently supplied by the VM agent. No merge or production deployment is performed by the test scripts.

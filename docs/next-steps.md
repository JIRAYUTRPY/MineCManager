# Foundation implementation roadmap

Current PR delivers a local agent and local panel prototype, with a first OpenAPI contract and integration coverage.

## Priority 0: before any deployment
- Run Go tests, vet and race tests in CI; review systemd privilege boundaries.
- Ensure agent commands cannot escape the configured unit or filesystem root.
- Add authenticated transport for remote nodes; avoid publishing loopback services.
- Define durable, fail-closed audit semantics for privileged mutations.
- Replace flat-file secrets with managed key lifecycle and scoped revocation.

## Priority 1: core control plane
- Persistent node enrollment, per-node identity and rotation.
- API key issuance, expiration, revocation, role-to-scope mapping.
- Postgres-backed audit log, retention, request IDs, pagination.
- Agent heartbeat, health checks, metrics and timeout policies.
- Plugin inventory, upload, validation, versioned edits and rollback.
- Backup manifests for worlds, configs, plugins, whitelist, ops, permissions and server metadata; verified restore.

## Priority 2: user interface
- Authenticated admin panel with granular actions and audit history.
- Player inventory inspector and controlled item edits via Paper plugin.
- World item indexing and analytics with asynchronous collection.
- OpenAPI generated SDK and end-to-end acceptance tests.

No VM has been configured and no production rollout is authorized by this PR.

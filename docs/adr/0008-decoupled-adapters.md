# ADR-0008 — Decoupled adapters, no marketplace

- Status: Accepted

## Decision
- An internal Go interface for adapters (`internal/adapter`) with a manifest (id, version, supported interface/protocol versions, platforms, actions, parameter schemas, permissions, confirmation level) validated against `api/schemas/catalog/adapter-manifest.json`.
- A public network API for independent remotes and receivers.
- Extensibility is demonstrated with the simulated backend (`sim`) and an independent Python controller.

## Alternatives
Loading dynamic plugins (Go `plugin`, shared libraries) or downloading code: large attack surface and poor portability.

## Consequences
No remote code or dynamic binaries are loaded. An adapter compiled into `relevo-host` runs with the process's privileges and is trusted code.

## Revisit when
Third-party adapters are needed without recompiling; a local IPC protocol would need its own contract and authentication.

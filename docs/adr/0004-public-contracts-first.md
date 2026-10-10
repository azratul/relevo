# ADR-0004 — Public contracts before private interfaces

- Status: Accepted

## Context
Third parties should be able to build remotes, receivers and adapters without reading the relay's code.

## Decision
- `api/openapi.yaml` (HTTP), `api/asyncapi.yaml` (WebSocket and DataChannel messages) and `api/schemas/` (JSON Schema) are the source of truth. `docs/protocol.md` specifies the semantics and defers to the schemas for shape.
- The relay **validates every inbound message at runtime** against the same schemas (embedded with `go:embed`), and tests validate everything the relay sends.
- `api/examples/valid` and `api/examples/invalid` are checked in CI with two independent validators (Go and Ajv) and against the runtime decoder.

## Alternatives
- Exporting TypeScript types: useless for other languages and silent about semantics.
- Generating schemas from Go structs: the contract would follow the implementation.

## Consequences
Any change visible to third parties must update schemas, examples and docs together; CI enforces it. An independent Python controller (`examples/python`) proves the contract is enough.

## Limits
Schemas describe shape; state rules (who may send what, when) live in `docs/protocol.md` and in integration tests.

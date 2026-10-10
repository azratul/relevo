# ADR-0012 — OpenAPI/AsyncAPI versions and a common JSON Schema dialect

- Status: Accepted

## Context
OpenAPI 3.1 defaults to a JSON Schema 2020-12 based dialect. AsyncAPI 3.0 requires JSON Schema draft-07 support (`application/schema+json;version=draft-07`). Sharing one file between both can be interpreted differently.

## Decision
- OpenAPI **3.1.1** and AsyncAPI **3.0.0**.
- Files in `api/schemas/` use the **common subset** of draft-07 and 2020-12: no `$schema` (the referencing document sets the dialect), `$defs` only as a container referenced by JSON pointer, and no keywords whose meaning differs between drafts (array-form `items`, `prefixItems`, `unevaluated*`, `dependentRequired`, `$dynamicRef`, `$recursiveRef`, `$anchor`).
- AsyncAPI references them with `schemaFormat: application/schema+json;version=draft-07`; OpenAPI references them directly.
- CI validates every example with Ajv in **both** dialects and with the Go validator in 2020-12; the verdicts must match. A script rejects forbidden keywords.

## Consequences
Third parties can use any draft-07 or 2020-12 validator and get the same verdict.

## Revisit when
AsyncAPI formally supports 2020-12.

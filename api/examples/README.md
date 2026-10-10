# Contract examples

- Files under `valid/` must validate; files under `invalid/` must fail. Both are checked in CI (Go: `go test ./internal/protocol/`; Ajv with draft-07 and 2020-12: `make contracts`).
- File name: `<schema>[--variant].json`, where `<schema>` is the file name in `api/schemas/ws/` or `api/schemas/http/`.
- Identifiers, tokens and passwords in the examples are fictitious.

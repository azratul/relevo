# Building remotes, receivers and adapters

Relevo separates three extensible pieces ([ADR-0008](adr/0008-decoupled-adapters.md)):

| Piece | What it does | Examples | Interface |
|---|---|---|---|
| **Controller** | Discovers actions and requests them | the web remote, a CLI, a hardware clicker | Public HTTP + WebSocket/DataChannel contract (controller role) |
| **Receiver** | Runs commands on the host's computer | `relevo-host`, a future browser extension | Public contract (host role) |
| **Action adapter** | Turns a semantic action into a local operation | the generic keyboard adapter, an app integration | Internal Go interface of `relevo-host` |

Controllers **need no Relevo code**: only [`api/openapi.yaml`](../api/openapi.yaml), [`api/asyncapi.yaml`](../api/asyncapi.yaml), [`api/schemas/`](../api/schemas) and [`docs/protocol.md`](protocol.md). Proof: [`examples/python/relevo_client.py`](../examples/python/relevo_client.py), a controller written with the Python standard library only (including a minimal WebSocket client).

## Writing a controller

1. **Login**: `POST /api/v1/sessions/{id}/join` with `{"displayName","password"}` (the `id` is the last segment of the `/s/{id}` link). Keep the `token` in memory only.
2. **WebSocket**: open `wss://<host's public URL>/api/v1/ws` (no token in the URL). First message:
   ```json
   {"protocolVersion":"1.0","type":"auth.hello","messageId":"<uuid>","sessionId":"<id>","payload":{"token":"<token>"}}
   ```
   You receive `auth.ok`, then `session.snapshot` (your state in `you`, the catalog in `catalog`).
3. **Send an action**: `command.request` with `commandId` (a new UUID), `clientSequence` (an integer that strictly increases for the token's lifetime), `action`, `parameters` (valid against `catalog.actions[].parameters`) and the latest snapshot's `catalogRevision`.
4. **Result**: `command.accepted` (with `sequence`) and then `command.result` with `status` and `confirmation`; or `command.rejected`. Correlate by `commandId` (or `correlationId`).
5. **Rules you MUST follow**: never retry a command whose result is `unknown`; never resend old commands after reconnecting (use `command.status.request` to learn what happened); update `catalogRevision` on every `session.updated`; ignore unknown fields in relay messages; send periodic `ping`s if your platform cannot answer WebSocket pings.
6. **WebRTC (optional)**: if `auth.ok.capabilities` contains `webrtc`, you may open a direct DataChannel with `POST /api/v1/sessions/{id}/rtc` ([protocol](protocol.md#webrtc-datachannel-transport-optional-adr-0014)). If it fails, keep using the WebSocket; never resend commands when switching transport.
7. **Browsers**: a page must be served from an allowed Origin (the host's public URL). Native clients send no Origin.

Quick test:

```sh
python3 examples/python/relevo_client.py controller --url https://<tunnel>/s/<id> --name Ana   # asks for the password
python3 examples/python/relevo_client.py controller --url ... --action presentation.next --count 20 --interval 0.3
```

## The host role

`relevo-host` implements the host side of the contract against its embedded relay. An alternative receiver (for example a browser extension for Google Slides) would implement the same messages:

1. Authenticate with the host token (`auth.hello`); after `auth.ok`, send `host.register` with `agentInstanceId`, `ready`, `target {label, focusCheck}` and its `catalog` (revision ≥ 1; restricted parameter schemas, see [protocol §8](protocol.md#8-action-catalog)).
2. Keep the `epoch` from `host.registered`.
3. For every `command.dispatch`: drop it if `epoch` is not current or `sequence` is not greater than the last one; revalidate against the current state (`session.updated` → `paused`; `participant.updated` → state and capabilities); check `validForMs` with a monotonic clock from reception; execute; reply with **one** `command.result` (`executed` with an honest `confirmation`, or `rejected`/`failed`/`expired` with `error`). If execution may be partial, `failed` + `EXECUTION_UNKNOWN`.
4. Use the application `ping` as a lease: without a `pong` for a few seconds, stop executing and reconnect (backoff with jitter). A new epoch follows; never run anything from the previous one.
5. Catalog changes: `host.update` with a higher revision.

A browser extension cannot control native applications without a native component ([ADR-0002](adr/0002-native-receiver-and-install-free-remote.md)).

## Writing an adapter inside `relevo-host`

Two levels in [`internal/adapter`](../internal/adapter):

### a) A new keyboard backend (`KeyBackend`)

For a new platform or injection mechanism. Implement:

```go
type KeyBackend interface {
    Name() string
    Platform() string        // "linux/x11", "windows", ...
    FocusCheck() string      // protocol.FocusTargeted | FocusVerified | FocusUnverified
    Confirmation() string    // usually protocol.ConfirmInputInjected
    Permissions() []string   // human-readable OS permissions
    Check(ctx) error         // *ErrNotReady{Code, Reason} when a permission or session is missing
    Windows(ctx) ([]Window, error)
    Active(ctx) (Window, error)
    Send(ctx, w Window, c Chord) error
}
```

and register it in `init()` with `adapter.Register(BackendInfo{Name, Description, Status}, ctor)` in a file with the platform's build tag. The generic `Keyboard` adapter does the rest: profiles, the key whitelist, the target check according to `FocusCheck`, and error classification. Rules for `Send`:

- Return `adapter.Reject(code, …)` if nothing was done (e.g. the key is not in the keymap).
- Return an error wrapping `adapter.ErrUncertain` if some event may have reached the system before failing.
- Always release what you pressed (use `defer`), also on errors.
- Never elevate privileges or require root.

Add an integration test that runs against a disposable environment (see `x11_unix_test.go` with Xvfb) and update [platform-support.md](platform-support.md).

### b) A full adapter (`Adapter`)

For non-keyboard integrations (e.g. an application API that can verify the effect and return `effect-verified`):

```go
type Adapter interface {
    Manifest() Manifest
    Catalog() []protocol.ActionDescriptor
    Readiness(ctx) Readiness
    Execute(ctx, action string, params json.RawMessage) (confirmation string, err error)
}
```

The manifest follows [`api/schemas/catalog/adapter-manifest.json`](../api/schemas/catalog/adapter-manifest.json): `id`, `version`, `interfaceVersions` (currently `[1]`, constant `adapter.InterfaceVersion`), `protocolVersions`, `platforms`, `actions` with parameter schemas, `permissions`, `confirmation` and `focusCheck`. The host validates parameters again before calling `Execute`; the relay already validated them with the same schema.

### Trust boundary

An adapter compiled into `relevo-host` runs with all the process's privileges: it is trusted code. No dynamic plugins are loaded and no code is downloaded.

## Conformance

- Validate your messages against `api/schemas` with any draft-07 or 2020-12 validator (the subset is common, ADR-0012). `api/examples/valid` and `invalid` are ready-made test cases.
- The relay's tests validate every message it sends against the schemas (`ValidateOutbound`), so what you receive from the official relay matches them.
- Test your client against a local host: `relevo-host --backend sim --tunnel url:http://127.0.0.1:8787 --local-port 8787 --allow-insecure-tunnel --start` (loopback only; the printed link points to your computer).

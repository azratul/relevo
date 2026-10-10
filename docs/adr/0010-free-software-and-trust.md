# ADR-0010 — Free software and the trust boundary

- Status: Accepted

## Decision
MIT licence for our code; dependency licences reviewed (`docs/dependencies.md`); no telemetry. HTTPS/WSS protects the transport. The relay runs on the host's own computer.

## Consequences
- The **tunnel provider** is the third party to trust: it terminates TLS, sees the traffic that crosses it (login, commands when not peer-to-peer) and serves the remote's JavaScript.
- With a WebRTC DataChannel (ADR-0014) commands travel DTLS-encrypted between the browser and `relevo-host`, but because the provider serves the page this is **not** end-to-end encryption against a malicious provider, and Relevo does not claim it.
- A malicious provider could at most drive the actions in the announced catalog, never arbitrary keys: the host only executes its local catalog.

## Alternatives
End-to-end encryption between remote and host (for example a PAKE derived from the password) needs a separate design, review and ADR; no improvised cryptography.

## Revisit when
Sessions with sensitive content require protection against the tunnel provider.

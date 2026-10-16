# Security policy

## Reporting a vulnerability

Please do not open a public issue. Contact the maintainer (see the git history) with a description, steps to reproduce and the version. Initial answer within 7 days.

## Security model (summary)

The normative details are in [docs/protocol.md](docs/protocol.md) and ADRs 0001, 0003, 0005, 0007, 0010 and 0015.

**What Relevo protects**

- A leaked link does not grant control: the password is needed, it travels through another channel and never appears in URLs, QR codes, logs or analytics.
- Only a salted Argon2id hash of the password is kept (OWASP parameters, bounded concurrency), with per-IP, per-session+IP (exponential lockout) and global limits.
- Every speaker gets an individual, opaque, revocable and expiring token; the host has a separate credential that never reaches speakers' browsers and is not derived from the password.
- Sessions cannot be created from the Internet: the embedded relay requires an internal creation token known only to `relevo-host`.
- Authorization is checked again for every command, in the relay and in the host; pause, removal, password rotation and ending the session take effect immediately.
- Speakers can only request **catalog actions** offered by the host. Keys are resolved locally from a whitelist: no arbitrary keyboard, mouse, shell or file transfer.
- The host checks that the target window has focus (or sends directly to it) where the platform allows, and refuses otherwise.
- WebSocket and DataChannel: authentication in the first message with a deadline, exact Origin allow list, size and rate limits, no tokens in URLs.
- Headers: strict CSP without third-party scripts, `frame-ancestors 'none'`, `Referrer-Policy: no-referrer`, HSTS over HTTPS. The host's local panel listens on loopback only, requires a random key and checks `Host` (DNS rebinding) and `Origin`.
- Built-in SSH tunnels never use your SSH keys or agent and pin the server's host key.

**What it does not protect**

- The **tunnel provider** terminates TLS, sees what crosses the tunnel and serves the remote's JavaScript. There is no end-to-end encryption against it; a direct WebRTC channel does not change that because the page still comes through the tunnel.
- bore.pub has no TLS at all; it is offered only with explicit consent.
- If link **and** password leak together, anyone can join until the host removes them, enables manual approval or rotates the password.
- The display name is not an identity.
- A command that already ran cannot be undone; there is a millisecond race between checking focus and injecting, and between removing a speaker and executing.
- An adapter compiled into the host has the process's privileges.
- Backends without focus checks (portal, wtype outside Hyprland) only work if the host explicitly enables the unchecked active-window mode.

## Supported versions

Only the main branch while the project is at 0.x.

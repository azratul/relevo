# ADR-0001 — Embedded relay published through a host-chosen tunnel

- Status: Accepted

## Context
Speakers send tiny commands (next/previous) to the computer of the person sharing the screen, which is usually behind NAT, a firewall or CGNAT. The owner does not want to run or pay for any server. Managed tunnels (serveo, localhost.run, Cloudflare quick tunnels, ngrok, Tailscale Funnel…) can publish a local port over HTTPS without opening ports.

## Decision
- `relevo-host` runs the **relay inside its own process**, listening on `127.0.0.1` only. The host agent talks to it over loopback.
- When creating a session the host chooses how speakers reach it (panel or `--tunnel`; the choice can be remembered):
  1. a tunnel provider started by `relevo-host` (serveo, localhost.run and bore.pub are built in, see ADR-0015; cloudflared, ngrok and Tailscale Funnel launch their own program);
  2. a custom command (Pangolin/newt, zrok, frp…) with a `{port}` placeholder and a regular expression for the public URL;
  3. the host's own URL, when they already run a tunnel or proxy to a fixed local port.
- The application protocol is independent of the transport. Speakers use HTTPS + WebSocket through the tunnel (one JSON message per text frame) and may upgrade to WebRTC (ADR-0014).
- The embedded relay serves a single session; session creation requires an internal random token, so nobody on the Internet can create sessions; `X-Forwarded-For` is trusted only from loopback (the tunnel client).
- If the tunnel dies it is restarted with backoff; when its public URL changes the relay adopts it and the panel tells the host to share the new link.
- Tunnels without TLS (bore.pub) require explicit consent, since the password and commands would travel in clear text.

## Alternatives
- A public relay run by the project: contradicts the requirement of having no server.
- WebRTC only: the remote page and the signalling still need a reachable HTTPS origin.
- A custom transport over QUIC: QUIC does not solve NAT traversal and UDP is often blocked.

## Consequences
- Nothing to operate; a single binary. The host depends on a third party (the tunnel provider), which terminates TLS and sees the traffic that crosses it (ADR-0010).
- Verified providers: serveo and ngrok pass the client IP; localhost.run and bore do not. Without client IPs every speaker shares one rate-limit key, so lockouts after failed logins are capped at 10 s and manual approval is recommended.
- Free plans of serveo and ngrok show a warning page that speakers accept once.
- Quick-tunnel URLs are random and short-lived: every session (or tunnel restart) has a new link.

## Limits
Free tunnels have no uptime guarantee. Some networks block WebSocket or specific tunnel domains; the remote shows a clear "reconnecting" state.

## Revisit when
A provider changes its output or terms (update `internal/tunnel` and the support matrix), or a permanent public endpoint becomes a requirement.

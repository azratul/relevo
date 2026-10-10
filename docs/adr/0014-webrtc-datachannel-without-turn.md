# ADR-0014 — WebRTC DataChannel with tunnel fallback, no TURN

- Status: Accepted

## Context
A direct browser ↔ host connection is preferable when the network allows it. WebRTC needs an HTTPS origin for the page, signalling, STUN to discover addresses and, when NAT forbids a direct path, TURN (a relay server someone must run and pay for).

## Decision
- The tunnel (ADR-0001) serves the page, the login and the signalling. After authenticating over WebSocket, the remote tries an **RTCDataChannel** (`relevo`, reliable and ordered) with `POST /api/v1/sessions/{id}/rtc`: an SDP offer with all candidates → an answer with all candidates (no trickle ICE).
- **Free public STUN** for address discovery (by default `stun:stun.l.google.com:19302` and `stun:stun.cloudflare.com:3478`; configurable). STUN relays no traffic.
- **No TURN**: if ICE fails, everything stays on the tunnel's WebSocket, which already crosses any NAT. The tunnel plays the role TURN would play.
- The DataChannel carries **the same protocol messages**, starting with `auth.hello`; once it authenticates, the relay replaces the speaker's WebSocket (close code 4009). If the peer-to-peer channel drops, the remote falls back to the tunnel automatically and queries its pending commands; it never resends them.
- Go side: `pion/webrtc` v4 (MIT) inside the embedded relay.

## Alternatives
- Managed TURN (e.g. Cloudflare Realtime TURN): cost and credentials for a case the tunnel already covers.
- WebRTC as the only transport: would fail on symmetric NAT and corporate networks without TURN.

## Consequences
- Where the network allows it, commands do not go through the tunnel provider and latency drops by one hop.
- Ordering, deduplication, epochs and the `unknown` state are protocol properties and do not change with the transport.

## Limits
Without TURN, ICE fails when both sides are behind symmetric NAT or UDP is blocked; then the tunnel is used. Upgrade and fallback were verified locally (including a simulated "no route" case and a dropped channel); the ICE success rate between different real networks has not been measured yet.

## Revisit when
Peer-to-peer is needed where ICE fails and the tunnel is not enough: add configurable TURN (the `iceServers` structure already supports it).

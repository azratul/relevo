# ADR-0009 — Ephemeral state and explicit limits

- Status: Accepted

## Decision
Sessions live in memory inside `relevo-host`, with expiry (session TTL, absent host) and explicit limits (participants, connections, message size and rate, commands in flight). Host configuration is a local file. Quitting `relevo-host` ends the session; pending commands are never recovered. A tunnel failure does not end the session (the tunnel is restarted), but may change the link.

## Alternatives
Persisting sessions: more moving parts and ambiguous states after restarts, for no real benefit in a single meeting.

## Consequences
Trivial operation and predictable behaviour. The limits are documented in `docs/protocol.md`.

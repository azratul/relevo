# ADR-0003 — Per-session password and separate roles

- Status: Accepted

## Context
The link is shared in chats that may be logged. The experience must not require accounts.

## Decision
- The link (`/s/{sessionId}`) carries **no authentication secret**; the session id grants nothing.
- The password is independent: generated with a CSPRNG (≈59 bits, `xxxx-xxxx-xxxx`) or chosen by the host (≥ 12 characters). The relay keeps only a salted Argon2id hash.
- The host holds a separate **host credential** (`hostToken`), returned only at session creation, never sent to speakers' browsers and not derived from the password.
- Each speaker who knows the password gets an individual, opaque, revocable, expiring token scoped to the session and the controller role.
- Optional manual approval: with it, the password only lets a speaker wait as `pending`.

## Alternatives
- A secret in the link fragment: leaking the link would grant control.
- Accounts/OAuth: out of scope and against a lightweight experience.

## Consequences
- Leaking only the link does not allow control.
- The shared password does not identify people; the display name is a label. If link and password leak together anyone can join until the host revokes, enables approval or rotates the password (which removes every speaker).

## Limits
No verified identity and no MFA. The relay processes the plaintext password during login (it runs on the host's own computer).

## Revisit when
Verified identities become a requirement (an optional OIDC layer could be added without breaking passwords).

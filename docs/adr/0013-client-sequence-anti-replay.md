# ADR-0013 — Client sequence for replay-safe deduplication

- Status: Accepted

## Context
Deduplicating only by `commandId` with a bounded cache lets an old, evicted command be accepted and executed again. The speaker's clock cannot be trusted to decide freshness.

## Decision
- Every `command.request` carries `clientSequence`, strictly increasing per **speaker token** (it survives reconnections of the same token).
- The relay keeps the last N records per speaker (256 by default). A known `commandId` with the same content returns the existing status; with different content → `COMMAND_ID_CONFLICT`. An unknown `commandId` with `clientSequence` ≤ the last one seen → `COMMAND_EXPIRED` (never executed).
- The host requires the relay-assigned `sequence` to increase within each `epoch` and rejects other epochs.
- Freshness uses relative budgets (`validForMs`) measured with each side's monotonic clock, never by comparing wall clocks.

## Alternatives
UUIDv7 with the client's timestamp: depends on the speaker's clock.

## Consequences
No resent command can run twice, with or without cache eviction. A client that loses its counter (page reload) needs a new token (a new login), which is how the official remote behaves.

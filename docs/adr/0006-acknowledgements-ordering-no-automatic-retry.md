# ADR-0006 — Acknowledgements, ordering and no automatic re-execution

- Status: Accepted

## Context
A duplicated "next" skips a slide; a lost one forces a repeat. Networks fail between execution and confirmation.

## Decision
- Three levels are distinguished: **accepted** by the relay (`command.accepted`), **executed** by the host (`command.result`, `status=executed`) and **verified** in the application (`confirmation=effect-verified`, only for capable adapters).
- The relay assigns a **total order per session**; the host executes serially and requires increasing sequences within each connection **epoch**.
- Deduplication by `commandId` in the relay (per speaker) and the host (per epoch), combined with monotonic sequences (ADR-0013), so cache eviction can never cause a re-execution.
- A command whose effect is uncertain is reported as `unknown` (`EXECUTION_UNKNOWN`) and **never retried automatically**.

## Alternatives
- A persistent queue replayed on reconnection: "ghost" slide changes minutes later.
- Promising exactly-once: impossible for external effects.

## Consequences
- Expired commands, or commands from older connections/epochs, are never replayed.
- The remote shows rejections and uncertainty; the speaker decides whether to press again.

## Limits
For keyboard injection, `executed` means "the OS accepted the input events", not "the slide changed".

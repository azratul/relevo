# ADR-0002 — Native receiver and install-free remote

- Status: Accepted

## Context
Only the host needs to affect their computer. Speakers change at every event and must not install anything.

## Decision
Only `relevo-host` is installed. Speakers use the web remote served by the host's embedded relay through the tunnel. A future browser extension (e.g. for Google Slides) would be an **alternative receiver** implementing the same host-side contract (`api/asyncapi.yaml`).

## Alternatives
- A mobile or desktop app for speakers: unacceptable friction.
- A browser extension as the only receiver: it cannot control native applications (PowerPoint, PDF viewers) without a native component.

## Consequences
- Adapters and permissions are needed per operating system (ADR-0007).
- The protocol is not designed around any input-injection library: the host announces semantic actions (ADR-0005) and translates them locally.

## Revisit when
Most use moves to web-based presentations, which would favour an extension receiver.

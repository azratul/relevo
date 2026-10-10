# ADR-0005 — Semantic actions and least privilege

- Status: Accepted

## Context
Giving remote people a keyboard is close to giving them a shell.

## Decision
The host announces a revisioned **catalog** of allowed actions. Each action has a namespaced id (`presentation.next`), title, description, parameter JSON Schema, the **capability** it requires (`presentation.navigate`) and the confirmation level it can provide. The host translates actions into keys or calls locally; speakers never send keys.

## Alternatives
- Sending key codes from the remote: arbitrary keyboard access.
- A fixed list of actions in the relay: would couple the relay to specific applications.

## Consequences
- Adapters can be added without exposing arbitrary input.
- Changing keys, targets or profiles is a **local, administrative** operation that does not exist in the speaker API.
- The relay checks that the action exists in the current catalog, that the revision matches and that the speaker holds the capability; the host checks again.

## Limits
An action translated into a key inherits that key's effect in whatever application receives it (see ADR-0007).

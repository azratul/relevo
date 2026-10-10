# ADR-0007 — Target control and capabilities per platform

- Status: Accepted

## Context
Generic key injection goes to the focused window, which may not be the presentation (for example, the video call's chat).

## Decision
- Prefer a **targeted** backend that addresses the window directly when one exists (Hyprland `send_shortcut` with a window selector).
- For generic injection, **verify** before every command that the configured target has focus (X11 `_NET_ACTIVE_WINDOW`, Windows `GetForegroundWindow`, macOS `frontmostApplication`, Hyprland IPC) and reject with `TARGET_NOT_ACTIVE` otherwise.
- Where focus cannot be checked (generic Wayland, the RemoteDesktop portal) the backend stays **not ready** unless the host explicitly enables `allowUnverifiedActiveWindow`; the UI says so.
- No privilege escalation and no root. Missing permissions (macOS Accessibility, portal consent) are reported as the "not ready" reason.

## Alternatives
- Always inject blindly: unsafe.
- uinput/ydotool by default: requires device permissions and knows nothing about focus.

## Consequences
There is a millisecond race between checking focus and injecting; absolute isolation is not promised. Real support is recorded per OS/backend/feature in `docs/platform-support.md`.

## Limits
Windows UIPI blocks injection into higher-integrity (elevated) processes. macOS requires the Accessibility permission. Wayland has no standard focus API.

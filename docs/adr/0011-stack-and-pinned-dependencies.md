# ADR-0011 — Stack and pinned dependencies

- Status: Accepted

## Context
Go for the relay and host, TypeScript + Vite for the remote. Each dependency was checked for maintenance, licence and platforms; versions with at least about two weeks since release were preferred.

## Decision
| Use | Dependency | Version | Licence | Why |
|-----|------------|---------|---------|-----|
| WebSocket | github.com/coder/websocket | v1.8.15 | ISC | Maintained, context-based API, read limits, no cgo |
| Argon2id, SSH client | golang.org/x/crypto | v0.57.0 | BSD-3 | Official Go project; `ssh` powers the built-in tunnels |
| JSON Schema at runtime | github.com/santhosh-tekuri/jsonschema/v6 | v6.0.3 | Apache-2.0 | draft-07 and 2020-12, configurable loader (remote refs blocked) |
| Validator messages | golang.org/x/text | v0.42.0 | BSD-3 | Formatting validation errors |
| WebRTC | github.com/pion/webrtc/v4 | v4.2.22 | MIT | Mature pure-Go WebRTC (DataChannel, ICE, mDNS) |
| Tray | fyne.io/systray | v1.12.2 | Apache-2.0 | Linux/BSD via D-Bus StatusNotifierItem without cgo; Windows without cgo; macOS needs cgo |
| X11 + XTEST | github.com/jezek/xgb | v1.3.1 | BSD-3 | Pure-Go X11 for Linux and BSD |
| D-Bus | github.com/godbus/dbus/v5 | v5.2.2 | BSD-2 | Portal and tray detection. v5.2.3 does not build for Windows, and fyne/systray imports it there too |
| macOS without cgo | github.com/ebitengine/purego | v0.11.1 | Apache-2.0 | CoreGraphics/AppKit calls that cross-compile |
| Win32 | golang.org/x/sys | v0.48.0 | BSD-3 | `SendInput`, `GetForegroundWindow` |
| Remote | TypeScript 7.0.2 + Vite 8.3.1, no framework | | Apache-2.0 / MIT | The UI is small; no runtime dependencies |

## Alternatives
- robotgo: cgo on every platform, no cross-compilation.
- getlantern/systray: less maintained than the Fyne fork.
- gorilla/websocket: fine, but the context-based API was preferred.

## Consequences
`relevo-host` cross-compiles without cgo for Linux, Windows, macOS, FreeBSD, OpenBSD and NetBSD (`make cross` plus `go vet` per platform). The module requires Go 1.26, the minimum needed by `golang.org/x/*`. On macOS the tray exists only in a cgo build made on a Mac; otherwise the local panel is used.

## Revisit when
A dependency changes maintenance or licence, or a platform blocker is demonstrated.

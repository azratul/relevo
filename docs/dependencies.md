# Dependencies and licences

Our code: **MIT** ([LICENSE](../LICENSE)). Policy: stable versions pinned (`go.sum`, `package-lock.json`), at least about two weeks old where possible; no telemetry and no third-party scripts in the remote.

## Go modules in `relevo-host` (what is distributed)

Modules compiled into `relevo-host` (`go list -deps ./cmd/...` for linux, windows and darwin):

| Module | Version | Licence | Use | Only on |
|---|---|---|---|---|
| github.com/coder/websocket | v1.8.15 | ISC | WebSocket | |
| golang.org/x/crypto | v0.57.0 | BSD-3-Clause | Argon2id; SSH client for built-in tunnels | |
| github.com/santhosh-tekuri/jsonschema/v6 | v6.0.3 | Apache-2.0 | Runtime validation with the published schemas | |
| golang.org/x/text | v0.42.0 | BSD-3-Clause | Validator error messages | |
| github.com/pion/webrtc/v4 (+ ice, dtls, sctp, datachannel, stun, mdns, …) | v4.2.22 | MIT | WebRTC DataChannel ([ADR-0014](adr/0014-webrtc-datachannel-without-turn.md)) | |
| github.com/wlynxg/anet | v0.0.5 | BSD-3-Clause | Network interfaces (pion dependency) | |
| github.com/google/uuid | v1.6.0 | BSD-3-Clause | pion dependency | |
| golang.org/x/net, golang.org/x/time | v0.58.0, v0.14.0 | BSD-3-Clause | pion / x/crypto dependencies | |
| fyne.io/systray | v1.12.2 | Apache-2.0 | Tray | |
| github.com/godbus/dbus/v5 | v5.2.2 | BSD-2-Clause | RemoteDesktop portal, tray detection (and fyne/systray) | |
| github.com/jezek/xgb | v1.3.1 | BSD-3-Clause | X11 + XTEST | Linux/BSD |
| github.com/ebitengine/purego | v0.11.1 | Apache-2.0 | CoreGraphics/AppKit without cgo | macOS |
| golang.org/x/sys | v0.48.0 | BSD-3-Clause | Win32 (`SendInput`, …) | Windows |

All are compatible with MIT for binary distribution. Apache-2.0 requires keeping the licence notice: ship those modules' LICENSE/NOTICE files when packaging (backlog: generate `THIRD_PARTY_LICENSES` automatically).

godbus v5.2.3 does not build for Windows and fyne/systray imports godbus there too, hence v5.2.2 ([ADR-0011](adr/0011-stack-and-pinned-dependencies.md)).

## Optional external tunnel programs

Not shipped with Relevo, each under its own terms: cloudflared, ngrok, tailscale. serveo, localhost.run and bore.pub need no program (built in).

## Web remote

The production bundle has **no third-party dependencies** (`npm ls --omit=dev` is empty): only our code (about 20 KB of JavaScript and 3 KB of CSS before compression). Development tools:

| Package | Version | Licence |
|---|---|---|
| vite | 8.3.1 | MIT |
| typescript | 7.0.2 | Apache-2.0 |
| playwright-core (E2E only; uses the system's Chrome/Firefox) | 1.63.0 | Apache-2.0 |

## Contract tools (`tools/contracts`, CI only)

@redocly/cli 2.57.0 (MIT), @asyncapi/parser 3.6.3 (Apache-2.0), ajv 8.20.0 (MIT), yaml 2.9.1 (ISC). Licence summary of the full tree (license-checker): 133 MIT, 21 Apache-2.0, 3 0BSD, 2 ISC, 1 BSD-3-Clause, 1 BSD-2-Clause, 1 Python-2.0 (`argparse`, a development dependency). No strong copyleft; none of it is distributed.

## Examples

`examples/python/relevo_client.py`: Python 3 standard library only.

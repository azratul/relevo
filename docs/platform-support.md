# Platform support

Three things are often confused and are kept apart here: whether the **web remote** works in a browser, whether **`relevo-host` builds** for a system, and whether **real key injection** works for a specific system + backend combination. Being written in Go does not mean universal support.

Legend:

- **Tested**: actually run in this project, with the evidence given.
- **Tested (CI)**: covered by a job in `.github/workflows/ci.yml`.
- **Builds**: cross-compilation verified (`make cross`, `go vet` per GOOS/GOARCH). Not a functional test.
- **Not available**: no supported way exists; the reason is given.

## Web remote

| Browser | Status | Evidence |
|---|---|---|
| Google Chrome (desktop, headless) | Tested | E2E `web/controller/e2e/run.mjs`, 15/15 |
| Chrome with mobile touch emulation (390×844, `tap`) | Tested | Same E2E (speaker B) |
| Firefox 157 (desktop, headless, WebDriver BiDi) | Tested | Same E2E with `BROWSER=firefox`, 15/15 |
| Safari / iOS Safari | Not tested | Expected to work (`fetch`, `WebSocket`, `RTCPeerConnection`; no exotic APIs) |
| Chrome Android, Firefox Android | Not tested | Same; a real phone was used manually by the owner through serveo |

Keyboard: shortcuts only while the page has focus; never inside input fields; auto-repeat ignored; Enter/Space on a focused button is a single click (tested). Touch: large buttons with `touch-action: manipulation` (tested with emulation).

## Tunnels ([ADR-0001](adr/0001-embedded-relay-and-host-chosen-tunnel.md), [ADR-0015](adr/0015-built-in-tunnel-clients.md))

serveo, localhost.run and bore.pub are **built into `relevo-host`** (nothing to install); the others launch their own program. Tested on 2026-10-16 from a home network in Chile, with `internal/tunnel/live_test.go` (HTTP + WebSocket through the tunnel) and `web/controller/e2e/live-tunnel.mjs` (panel → tunnel → Chrome through the public URL → WebRTC → 10 commands → end of session):

| Provider | Status | HTTPS | Client IP | WebSocket | Warning page | Time to URL | WS echo RTT |
|---|---|---|---|---|---|---|---|
| serveo.net — **built in** (own SSH client) | Tested (also with an empty `PATH`) | yes | yes | yes | yes, one click ("Serveo - Warning") | 2.3–2.8 s | ≈510 ms |
| localhost.run — **built in** | Tested (also with an empty `PATH`) | yes | **no** | yes | no | 1.8–2.3 s | ≈240 ms |
| bore.pub — **built in** (own bore protocol client) | Tested (also with an empty `PATH`) | **no** (`http://`) | no | yes | no | 0.4–0.8 s | ≈270 ms |
| ngrok 3.36.1 (free plan, with authtoken) | Tested | yes | yes | yes | yes, one click | 0.5–0.8 s | ≈105 ms |
| cloudflared (quick tunnel) | Implemented, **not tested** (not installed here) | — | — | — | — | — | — |
| Tailscale Funnel | Implemented, **not tested** | — | — | — | — | — | — |
| Custom command (Pangolin/newt, zrok, frp…) | Tested with a fake provider (including a restart with a new URL) | depends | — | — | — | — | — |

RTTs are WebSocket echoes through the tunnel; they depend on where the provider's servers are, not on Relevo.

## WebRTC (peer-to-peer)

| Case | Status |
|---|---|
| Upgrade to DataChannel in Chrome and Firefox (same machine) | Tested (E2E) |
| No ICE route → stays on the tunnel, everything works | Tested (E2E `NO_P2P=1`, the relay offers no candidates) |
| Peer-to-peer channel lost mid-session → back to the tunnel without executing anything | Tested (E2E) |
| Through real tunnels (ngrok, localhost.run, serveo) | Tested, with browser and host on the same machine (trivial P2P) |
| P2P between different networks behind real NATs (Google/Cloudflare STUN) | **Not measured** yet |

## relevo-host: build and tray

| System | Builds | Tray | Notes |
|---|---|---|---|
| Linux amd64/arm64 | Yes (no cgo) | Partially tested: the icon registers as a StatusNotifierItem with the bar's watcher (Hyprland + Quickshell), verified over D-Bus | Without an SNI watcher the host detects it and uses the local panel + terminal |
| Windows amd64/arm64 | Yes (no cgo) | Builds (fyne systray, Win32) | Tested (CI) once the `native-build` job runs |
| macOS amd64/arm64 | Yes without cgo (no tray) | Only with a cgo build made on a Mac | Without cgo: local panel in the browser + terminal |
| FreeBSD amd64 | Yes | Builds (SNI over D-Bus) | Not verified on real FreeBSD |
| OpenBSD, NetBSD | Yes | Builds | Not verified |

Without a tray the host is **never** invisible: it opens the local panel and listens for Ctrl+C / SIGTERM in the terminal, and the panel has "Quit Relevo".

## Input injection per backend

| Backend | Platform | Target check | Status | Evidence / reason |
|---|---|---|---|---|
| `sim` | any | simulated (focus on/off) | Tested | Go tests and E2E |
| `hyprland` | Linux, Hyprland 0.56 | **Targeted**: sends to the chosen window with `send_shortcut` over IPC; no focus needed | Tested | Smoke test on our own windows (see below) and a real zathura presentation |
| `wtype` | Linux, wlroots compositors (and Hyprland) | Checked only on Hyprland (IPC `activewindow`); unchecked on other wlroots compositors | Tested on Hyprland | Rejects with `TARGET_NOT_ACTIVE` when another window has focus; delivers with focus |
| `x11` | Linux/BSD with X11 (XTEST) | Checked with `_NET_ACTIVE_WINDOW` (EWMH) | Tested on Linux (isolated nested Xwayland); Tested (CI) with Xvfb | `TestX11Injection`: delivery to the target, rejection when focus moves, no stuck modifiers |
| `x11` | FreeBSD/X11 | Same | Builds | First BSD target; no evidence on real FreeBSD |
| `portal` | Linux Wayland with the RemoteDesktop portal (GNOME, KDE) | **Not available** (the portal does not report focus): requires `allowUnverifiedActiveWindow` | Builds | The Hyprland portal on this machine does not implement RemoteDesktop |
| `windows` | Windows | Checked with `GetForegroundWindow` + executable name | Builds | No Windows machine. UIPI blocks elevated processes |
| `darwin` | macOS | Checked with `NSWorkspace.frontmostApplication` (bundle id) | Builds | No Mac. Needs the Accessibility permission |
| uinput / ydotool | Linux | Not available | — | Needs access to `/dev/uinput` (group or root) and knows nothing about focus; against ADR-0007 |
| Generic Wayland (GNOME/KDE without portal) | Linux | Not available | — | Wayland does not let clients inject input or read focus without an authorised protocol or portal |

### Evidence from real tests (Linux, Hyprland 0.56.2)

With the owner's explicit permission and only on windows launched for the test (`wev` as the target, `kitty --class relevo-other` as "another window", recording what each received):

1. `hyprland`, focus on `relevo-other`: `presentation.next` (Page_Down) reached `wev` (`sym: Next`); `relevo-other` received nothing.
2. `wtype`, focus on `relevo-other`: rejected with `TARGET_NOT_ACTIVE`; nothing sent.
3. `wtype`, focus on `wev`: `presentation.previous` arrived as `sym: Prior`.
4. Target window closed: `hyprland` → "target window not found" (not ready).
5. 20 targeted sends in a row: 20/20 received; local execution p50 ≈ 0.2 ms.
6. Real application: zathura in presentation mode with a 5-page PDF, focus on another window; two speakers (independent Python client). The page, read through zathura's D-Bus interface after each command: 1→2→3→4→3→4→5; 20× previous → 1; 20× next → 5.

Method note: `wev` uses block buffering when writing to a file; use `stdbuf -oL`, otherwise counts are wrong.

## Keys

- Speakers never send keys: the host translates actions into a **whitelist** of logical names (X keysym names): `Left Right Up Down Page_Up Page_Down Home End space Return Escape BackSpace Tab period F1…F12 a…z 0…9`, with modifiers `ctrl alt shift super`. `ctrl+alt+Delete` and similar cannot be expressed.
- **Logical vs physical**: X11, wtype, Hyprland and the portal use keysyms (logical): `Right` is the right arrow on any layout. X11 looks the keycode up in the current map and adds Shift for level-2 keysyms. Windows uses virtual keys (logical for navigation, layout-dependent for letters) with the scan code from `MapVirtualKey`. macOS uses `kVK_*` codes, which are **physical ANSI positions**: navigation and function keys are layout-independent, letters and digits follow the ANSI position.
- That is why the default profiles only use navigation keys (`Right`/`Left` or `Page_Down`/`Page_Up`); `presentation.blank` (`b`) and `presentation.goto` (digits) may vary on macOS with non-ANSI layouts.
- Press and release are always paired; pressed modifiers are released even on errors (checked on X11 with `QueryKeymap`).

## Official APIs (summary and links)

- **Windows**: `SendInput` inserts events into the input stream and is subject to UIPI: a process cannot inject into one with a higher integrity level ([SendInput](https://learn.microsoft.com/windows/win32/api/winuser/nf-winuser-sendinput), [integrity levels](https://learn.microsoft.com/windows/win32/secauthz/mandatory-integrity-control)). Focus: [GetForegroundWindow](https://learn.microsoft.com/windows/win32/api/winuser/nf-winuser-getforegroundwindow). Relevo does not elevate: if PowerPoint runs as administrator, the host reports a failure.
- **macOS**: `CGEventPost` requires the process to be trusted for Accessibility (`AXIsProcessTrusted`) ([CGEventPost](https://developer.apple.com/documentation/coregraphics/1456527-cgeventpost), [AXIsProcessTrusted](https://developer.apple.com/documentation/applicationservices/1460720-axisprocesstrusted)). Focus: [frontmostApplication](https://developer.apple.com/documentation/appkit/nsworkspace/frontmostapplication).
- **X11**: the XTEST extension ([specification](https://www.x.org/releases/current/doc/xextproto/xtest.html)); focus from EWMH `_NET_ACTIVE_WINDOW` ([EWMH](https://specifications.freedesktop.org/wm-spec/latest/)). Does not work on native Wayland windows.
- **Wayland**: by design, regular clients cannot inject input or query focus. Authorised ways: the [RemoteDesktop portal](https://flatpak.github.io/xdg-desktop-portal/docs/doc-org.freedesktop.portal.RemoteDesktop.html) (with user consent; no focus information), compositor-specific protocols (wlroots `virtual-keyboard`, used by `wtype`) and compositor IPC (Hyprland). An X11 solution does not work on Wayland.
- **Hyprland**: IPC over the Unix socket `$XDG_RUNTIME_DIR/hypr/$HYPRLAND_INSTANCE_SIGNATURE/.socket.sock`; since 0.56 dispatchers use the Lua API (`hl.dsp.send_shortcut({mods, key, window})`). The backend tries the Lua syntax and falls back to the classic `sendshortcut MODS, key, window`, which has not been tested (only 0.56 was available).

## Manual validation guide

To mark a new combination as "Tested":

1. Build `relevo-host` and run `relevo-host backends` and `relevo-host check` (exit 0 = ready; otherwise it prints the missing permission).
2. `relevo-host windows` lists candidate windows; put the presentation's class in `target.class` (or pick it in the panel).
3. Open a **test document** (e.g. a 5-page PDF) and any other window.
4. With the presentation focused: `relevo-host try presentation.next` → it must advance and print `executed`.
5. Focus the other window: `relevo-host try presentation.next` → checked backends: `TARGET_NOT_ACTIVE` and nothing reaches the other window; targeted backends: the presentation advances without touching the other window.
6. Repeat with `presentation.previous`, `presentation.first` and a shortcut with modifiers if you use one; make sure no key stays pressed (type in another app afterwards).
7. Full test: `relevo-host --tunnel serveo --start`, join from two devices and alternate commands.
8. Record system, version, backend, application and results on this page.

# Status and backlog

## Milestones

| Milestone | Status | Delivered | Pending |
|---|---|---|---|
| 0. Feasibility | Done | Dependencies checked (version, release date, licence, platforms, [ADR-0011](adr/0011-stack-and-pinned-dependencies.md)); real spikes of tray, injection (Hyprland, wtype, X11) and focus detection; [support matrix](platform-support.md); ADRs | — |
| 1. Contract and simulator | Done | OpenAPI 3.1.1, AsyncAPI 3.0.0, 36 schemas, 68 examples, [protocol](protocol.md) with state machines; simulated backend; independent Python controller; CI validators (`tools/contracts`, Go tests) | — |
| 2. Vertical slice | Done | Embedded relay, session, password, separate credentials, host, web remote, next/previous; first native backend validated (Hyprland) with a real application (zathura) and two speakers; access without the password fails even through the API | — |
| 3. Reliability and control | Done | Total order, deduplication with `clientSequence`, epochs and fencing, monotonic validity, heartbeat/lease, `unknown`, pause, removal, rotation, end, limits; crash tests before/after executing and before the ACK with the real agent | Reconcile results from the previous epoch when the host survives a cut (fewer `unknown`) |
| 4. Desktop app and portability | Partial | Tray (Linux verified over D-Bus; Windows/macOS build), 4-step local panel, EN/ES, CLI, Windows/macOS/portal backends build, native CI builds configured, manual validation guide | Run CI; validate on real Windows, macOS, GNOME/KDE and FreeBSD; packaging (installers, signing, notarisation, third-party licences) |
| 5. No-server delivery and extensibility | Done | One binary with embedded relay, built-in tunnels (serveo, localhost.run, bore.pub) and WebRTC; independent client; adapter guide ([plugin development](plugin-development.md)) | — |
| 6. Validation and delivery | Done | [Tests and results](testing.md), [dependency review](dependencies.md), README, guides | — |

## Risks

1. **Free tunnels**: no uptime guarantee; they may change their output, add warning pages or limit usage. Mitigation: several providers, custom command and own URL.
2. **Fragmented Wayland**: no standard focus or injection API. Only Hyprland has a targeted mode; GNOME/KDE depend on the portal (not verified here) and need the unchecked mode.
3. **Untested platforms**: Windows and macOS only build; UIPI, Accessibility and key mapping may bring surprises.
4. **Injection ≠ effect**: `executed` for keys does not guarantee a slide change (dialogs, internal focus, different shortcuts).
5. **Tunnel provider in the trust base**: it sees what crosses the tunnel and serves the remote's JavaScript.
6. **Corporate networks**: some proxies block WebSocket or tunnel domains.

## Prioritised backlog

**P0 — before real-world use**
1. Test P2P between two real networks (mobile data ↔ home network) and measure the ICE success rate; test cloudflared and Tailscale Funnel.
2. Publish the repository and run CI (native Windows/macOS builds, X11 test with Xvfb, E2E); pin actions by SHA.
3. Manual validation on Windows (PowerPoint, LibreOffice; the UIPI case) and macOS (Keynote, PowerPoint; Accessibility permission) following [the guide](platform-support.md#manual-validation-guide).
4. Test the `portal` backend on GNOME and KDE.
5. Packaging: generated `THIRD_PARTY_LICENSES`, signed/notarised binaries (macOS), Windows installer, Linux packages, optional autostart.

**P1**
6. Targeted adapters with effect verification (`effect-verified`): zathura over D-Bus, LibreOffice Impress (UNO), PowerPoint (COM), Keynote (AppleScript/JXA).
7. Reconcile results after a host reconnection (report the previous epoch) to reduce `unknown` states.
8. A browser-extension receiver for Google Slides (same contract).
9. Safari/iOS and physical touch devices in the tests.
10. Screen-reader accessibility review; more languages.

**P2**
11. Configurable TURN for P2P where ICE fails (today the tunnel is the fallback); automatically try another tunnel if the chosen one fails.
12. End-to-end encryption design (new ADR).
13. Real FreeBSD; OpenBSD/NetBSD.
14. An authenticated local IPC protocol for external adapters.

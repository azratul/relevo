# Tests and results

Results on Arch Linux x86_64, Hyprland 0.56.2 (Wayland), Go 1.27.2 (the module requires ≥ 1.26), Node 26.11.1, Chrome stable and Firefox 157.0.1. CI runs on GitHub Actions (Linux, Windows and macOS).

## How to run

| What | Command | Needs |
|---|---|---|
| Unit + HTTP/WebSocket/WebRTC integration (Go) | `make test` (or `go test ./...`) | Go |
| Same with the race detector | `make test-race` | Go + cgo |
| Contracts (OpenAPI, AsyncAPI, schemas, examples under draft-07 and 2020-12) | `make contracts` | Node |
| E2E: host panel wizard, then remote in Chrome and Firefox (P2P, no-P2P fallback, EN/ES) | `make e2e` | Node, Chrome, Firefox |
| Real tunnels (opens public tunnels for a few seconds) | `RELEVO_TEST_TUNNELS=serveo,localhost.run,bore,ngrok go test -run TestLiveProviders ./internal/tunnel/` and `TUNNEL=serveo node web/controller/e2e/live-tunnel.mjs` | Internet |
| X11 injection on a **disposable** X server | `Xvfb :99 & RELEVO_TEST_X11_DISPLAY=:99 go test -run TestX11Injection ./internal/adapter/` | Xvfb or a nested Xwayland |
| Cross-compilation | `make cross` | Go |
| Reproducible latency | `tools/latency/measure.sh [one_way_ms] [jitter_ms] [count]` | Python 3 |
| Manual injection validation per platform | [platform-support.md](platform-support.md#manual-validation-guide) | A test desktop |

CI never injects real key presses except against Xvfb.

## Results

| Suite | Result |
|---|---|
| `go test ./...` | 46 tests PASS, 2 SKIP (X11 needs a disposable display; live tunnels are opt-in) |
| `go test -race ./...` | PASS |
| `TestX11Injection` against a nested Xwayland `:97` (`-count=3`) | PASS |
| `make contracts` | PASS: Redocly clean, AsyncAPI valid (21 messages), 36 schemas in the common subset, 68 examples with the same verdict under draft-07 and 2020-12, schema and error-code coverage |
| E2E host panel (4-step wizard, EN/ES, real visibility of each step, approval) | 6/6 |
| E2E Chrome (P2P upgrade, fallback when the P2P channel drops, EN/ES switch) | 15/15 |
| E2E Firefox (WebDriver BiDi) | 15/15 |
| E2E Chrome with no P2P route (stays on the tunnel) | 14/14 |
| Live tunnels: `TestLiveProviders` | serveo, localhost.run, bore, ngrok: PASS |
| Live tunnels: `live-tunnel.mjs` (panel → tunnel → Chrome → P2P → 10 commands → end) | built-in with an empty `PATH`: serveo, localhost.run, bore: PASS; external: ngrok: PASS |
| `make cross` + `go vet` per platform | linux/amd64, linux/arm64, windows/amd64, windows/arm64, darwin/amd64, darwin/arm64, freebsd/amd64, openbsd/amd64, netbsd/amd64: OK |
| Real injection smoke tests (Hyprland, wtype, zathura) | See [platform-support.md](platform-support.md#evidence-from-real-tests-linux-hyprland-0562) |
| Independent Python controller ↔ `relevo-host` | OK |

## Acceptance criteria

| # | Criterion | How it is verified | Status |
|---|---|---|---|
| 1 | A leaked link with a wrong password cannot read state or send actions | `TestLeakedLinkWithoutPassword` (direct HTTP and WebSocket; unknown session indistinguishable), E2E step 1 | Met |
| 2 | A speaker token cannot change permissions or keys, nor end the session | `TestParticipantCannotAdminister` (every host route → 403; `host.register` over WebSocket → FORBIDDEN). Keys do not exist in the API: they are local | Met |
| 3 | Unauthenticated sockets expire; foreign Origins and malformed messages are rejected | `TestUnauthenticatedTimeoutOriginAndMalformed` (timeout→4001, Origin→403, broken JSON, schema, version, size→1009); `TestWebRTCGuards` for DataChannels | Met |
| 4 | Two speakers, observable order, correlated results | `TestTwoSpeakersOrderedAndCorrelated`, `TestVerticalSliceTwoSpeakers`, E2E, zathura | Met |
| 5 | Repeating a `commandId` does not repeat the effect; a changed payload is rejected | `TestDeduplication` (includes an evicted command with an old `clientSequence` → `COMMAND_EXPIRED`) | Met |
| 6 | With the host disconnected no new commands; reconnecting does not run old ones | `TestHostOfflineAndNoReplay`, `TestHostOfflineThenReconnect` (real agent) | Met |
| 7 | A crash after executing and before the ACK gives explicit uncertainty and no retry | `TestCrashBeforeAckIsUnknown`, `TestCrashAfterExecuteBeforeAck` (real agent: the link is cut right after injecting; result `unknown`, executed exactly once after reconnecting) | Met |
| 8 | Pause, removal, password change and end of session revoke access | `TestPauseRevokeRotateClose`, `TestPasswordRotationKeepsHost`, `TestEndSession`, `TestHostRevalidates`, E2E | Met |
| 9 | Focus or permission changes are rejected where they can be checked; elsewhere the limitation is reported | `TestKeyboardFocusCheck` (sim), `TestX11Injection` (real X11), wtype/Hyprland smoke tests; the portal is `unverified` and not ready without opt-in | Met for sim, X11 and Hyprland; Windows/macOS only build |
| 10 | An independent client integrates using only docs and schemas | `examples/python/relevo_client.py` (standard library only) | Met |
| 11 | The remote works with keyboard, touch and target browsers, separating real tests from expectations | E2E Chrome (desktop + emulated mobile touch) and Firefox; Safari and physical devices not automated | Partial (see platform support) |
| 12 | Works by following the README, remote traffic encrypted, no secrets in logs or URLs | One binary; built-in HTTPS tunnels tested live; log checks in the E2E | Met (bore.pub, without TLS, only with explicit consent) |

## Latency

Round trip measured by the Python client from sending `command.request` to receiving `command.result` (not the relay's ACK), simulated backend, 4 commands/s, everything on one machine. `tools/latency/delayproxy.py` emulates the network between speaker and relay (fixed delay + uniform jitter in each direction, TCP order preserved); host and relay share a process, so there is no hop between them.

| Scenario | Emulated network RTT | n | p50 | p90 | p99 | max |
|---|---|---|---|---|---|---|
| Loopback | 0 | 100 | 0.8 ms | 0.9 ms | 1.0 ms | 28.6 ms |
| Loopback, Hyprland → real zathura | 0 | 40 | 1.1 ms | 1.7 ms | 1.9 ms | 2.0 ms |
| 40 ms each way + 0–10 ms jitter | ≈80 ms | 100 | 94.1 ms | 98.2 ms | 101.2 ms | 101.3 ms |
| 100 ms each way + 0–30 ms jitter | ≈200 ms | 60 | 235.3 ms | 252.6 ms | 256.6 ms | 256.7 ms |

Reading: Relevo's own cost is about 1 ms; the rest is the network between speaker and host (through the tunnel, or direct with P2P). This is not a universal Internet figure. For keys, `executed` means "key injected", not "slide changed"; in the zathura test the page change was checked separately over D-Bus.

## Bugs found by the tests (and fixed)

- macOS CI: closing a tunnel took 30 s. The client's child process survived SIGTERM (most likely ignored, inherited from the runner) and killing only the client left that child holding the output pipe. The whole process group now gets SIGKILL after 1 s and `WaitDelay` bounds the wait; `TestCloseWhenSIGTERMIsIgnored` reproduces it on Linux.

- Host panel: `section { display:grid }` overrode the `hidden` attribute and all four steps were visible at once. The tests only checked the attribute; screenshot review caught it. `[hidden]` is now `display:none !important` and the E2E asserts real visibility.
- Built-in SSH client: `x/crypto` rejects `forwarded-tcpip` channels whose address differs from the requested one (serveo and localhost.run announce another) → they are accepted directly; localhost.run never answers a want-reply `shell` → it is sent without waiting; a `known_hosts` entry of another key type looked like a mismatch → ED25519 is preferred and a new key type is pinned (a different key of the same type is still refused).
- localhost.run: the URL pattern matched a banner link (`admin.localhost.run`) instead of the tunnel.
- localhost.run does not send the client IP: with the original limits one person with the link could lock everyone out of logging in; the lockout is now capped at 10 s with such providers.
- Conflicting `GET /` and `/api/` routes in the relay's and panel's `ServeMux` (panic at start-up with the embedded remote); covered by `TestWebAndSecurityHeaders` and `TestAdminPanelGuards`.
- `participant.updated` sent before `auth.ok` (found by the independent Python client); covered by `TestAuthMessageOrder`.
- The remote rebuilt its DOM on every message, losing half-typed numbers and possibly swallowing a key press; it now updates in place (E2E regression step).
- The login form cleared the name after a failed attempt.
- The adapter manifest serialised `permissions: null` instead of `[]` (not valid against the public schema); covered by `TestManifestConformsToSchema`.
- godbus v5.2.3 does not build for Windows (found by cross-compilation); pinned to v5.2.2.

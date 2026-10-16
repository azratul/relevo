# Relevo

A presentation remote for video calls. The person sharing their screen runs a small program (`relevo-host`); the other speakers open a link, type the password and move slides forward or back from the browser on their computer, phone or tablet, without installing anything. The video call carries the picture; Relevo carries tiny commands.

Free software (MIT) with **no server**: no accounts, no telemetry and no Relevo machine anywhere on the Internet.

```
                         tunnel chosen by the host
 speakers (browser)      (serveo, localhost.run, bore.pub built in;      host's computer
┌──────────────────┐      cloudflared, ngrok, Tailscale, Pangolin…)  ┌──────────────────────────────┐
│ web remote       │ ── HTTPS: page, login, signalling ────────────► │ relevo-host                  │
│ "Next ▶"         │ ◄──────────── WebSocket (fallback) ──────────── │  ├ embedded relay (127.0.0.1)│
│                  │ ◄═══ direct WebRTC DataChannel (P2P) ═════════► │  ├ action catalog            │
└──────────────────┘     when the NAT allows it (public STUN)        │  └ local keys → window       │
                                                                     └──────────────────────────────┘
```

- **Nothing to run elsewhere**: `relevo-host` contains the relay and publishes it through the tunnel you pick when creating the session (or your own URL).
- **Peer-to-peer when possible**: after joining, the remote tries a direct WebRTC DataChannel (address discovery with free public STUN). If the NAT does not allow it, everything keeps using the tunnel, without TURN.
- The link carries no secret; the **password** goes through another channel. Every speaker gets an individual, revocable token.
- Speakers request **semantic actions** (`presentation.next`); the host decides locally which key that is and which window receives it. There is no arbitrary keyboard access.
- Each command shows whether it was rejected, executed or whether the outcome is **unknown** (it is never retried automatically).
- English by default, with an EN/ES switch in the host panel and in the remote.

## Install

Linux, macOS and FreeBSD:

```sh
curl -fsSL https://raw.githubusercontent.com/azratul/relevo/main/install.sh | sh
```

Windows (PowerShell):

```powershell
irm https://raw.githubusercontent.com/azratul/relevo/main/install.ps1 | iex
```

With Go ≥ 1.26:

```sh
go install github.com/azratul/relevo/cmd/relevo-host@latest
```

The scripts download the binary for your system from the [latest release](https://github.com/azratul/relevo/releases/latest), verify its SHA-256 checksum and install it in `~/.local/bin` (`%LOCALAPPDATA%\relevo` on Windows) without root. `RELEVO_VERSION=v0.1.0` installs a specific release and `RELEVO_INSTALL_DIR` changes the directory. Release binaries for macOS include the tray; the ones from `go install` include it too when cgo is available (the default on macOS with Xcode command-line tools).

## Usage

```sh
relevo-host                           # opens the panel: pick a tunnel (serveo by default) and create the session
```

Nothing else needs to be installed: serveo.net, localhost.run and bore.pub are built into the binary. The panel guides you in four steps: **Session** (how speakers connect) → **Target & actions** (the presentation window) → **Active session** (link and password to share) → **Speakers & activity**.

Without the graphical panel:

```sh
relevo-host --tunnel serveo --start        # or localhost.run, bore; ngrok/cloudflared/tailscale if installed
relevo-host --start                        # asks in the terminal which tunnel to use
relevo-host --tunnel url:https://my.domain --local-port 8787 --start   # your own tunnel/proxy
```

`relevo-host backends` lists the input backends and tunnels. The `sim` backend presses no real keys; to control a presentation pick a real backend and the target window (panel, or `relevo-host windows`). Full guide: [docs/host-guide.md](docs/host-guide.md).

## Components

| Component | Path | What it is |
|---|---|---|
| Host | `cmd/relevo-host`, `internal/hostagent`, `internal/adapter`, `internal/tunnel` | Tray + local panel + CLI; embedded relay; tunnels; keyboard adapters |
| Relay | `internal/relay` | HTTP + WebSocket + WebRTC inside `relevo-host`; sessions, Argon2id login, routing |
| Web remote | `web/controller` | TypeScript + Vite, no framework and no runtime dependencies |
| Contract | `api/` | OpenAPI, AsyncAPI, JSON Schema and examples (source of truth) |
| Independent client | `examples/python` | A controller written only from the contract (standard library) |

## Platform support (summary)

| | Linux | Windows | macOS | FreeBSD |
|---|---|---|---|---|
| Web remote | Chrome and Firefox tested (E2E, including WebRTC); Safari not tested | | | |
| `relevo-host` builds | Yes | Yes | Yes (tray only with cgo) | Yes |
| Real injection | **Tested**: Hyprland (targeted, no focus needed), wtype (Hyprland), X11/XTEST. GNOME/KDE portal: builds | Builds | Builds | Builds |
| Tunnels tested live | built in: serveo, localhost.run, bore.pub (no TLS); external: ngrok. cloudflared and Tailscale: not tested | | | |

Details and evidence: [docs/platform-support.md](docs/platform-support.md).

## Documentation

- [Host guide](docs/host-guide.md): installation, tunnels, permissions, usage, troubleshooting.
- [Protocol](docs/protocol.md) (normative) · [OpenAPI](api/openapi.yaml) · [AsyncAPI](api/asyncapi.yaml) · [Schemas](api/schemas).
- [Building remotes, receivers and adapters](docs/plugin-development.md).
- [Platform and tunnel support](docs/platform-support.md) · [Tests and results](docs/testing.md) · [Status and backlog](docs/plan.md).
- [Architecture decision records](docs/adr/README.md) · [Dependencies and licences](docs/dependencies.md) · [Security](SECURITY.md).

## Compatibility

- Protocol **1.0**, independent of the application version; HTTP API under `/api/v1`. The same protocol runs over WebSocket and WebRTC DataChannel.
- The session lives in `relevo-host`: quitting it ends the session. Quick tunnels give a new link for every session.

## Development

Building from source needs Go ≥ 1.26 and Node ≥ 22:

```sh
make web host    # bin/relevo-host

make test        # Go: unit + HTTP/WebSocket/WebRTC integration
make contracts   # OpenAPI, AsyncAPI, schemas and examples (draft-07 and 2020-12)
make e2e         # host panel + remote in real Chrome/Firefox: P2P, tunnel fallback, EN/ES
TUNNEL=serveo node web/controller/e2e/live-tunnel.mjs   # optional live test through the Internet
```

Releases: pushing a `vX.Y.Z` tag runs `.github/workflows/release.yml`, which builds the archives (`tools/release/build.sh`; macOS on a Mac runner with cgo), writes `checksums.txt` and publishes a GitHub release. The built web remote is committed in `web/controller/dist` so that `go install` can embed it; CI fails if it does not match the sources (`make web`).

## Licence

MIT. See [LICENSE](LICENSE) and [dependencies](docs/dependencies.md).

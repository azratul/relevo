# Host guide (`relevo-host`)

## Install

It is **a single binary**: the relay, the web remote, the local panel and the built-in tunnels are all inside.

```sh
curl -fsSL https://raw.githubusercontent.com/azratul/relevo/main/install.sh | sh     # Linux, macOS, FreeBSD
irm https://raw.githubusercontent.com/azratul/relevo/main/install.ps1 | iex          # Windows (PowerShell)
go install github.com/azratul/relevo/cmd/relevo-host@latest                          # with Go ≥ 1.26
```

The scripts verify the SHA-256 checksum of the release archive and install without root (`~/.local/bin`, or `%LOCALAPPDATA%\relevo` on Windows). Binaries are not signed yet: on Windows SmartScreen may warn the first time; on macOS a binary downloaded with a browser is quarantined by Gatekeeper (the install script uses curl, so it is not).

From source: `make web host` (Go ≥ 1.26 and Node ≥ 22) produces `bin/relevo-host`.

The install scripts also register the app (skip with `RELEVO_NO_DESKTOP=1`):

| System | Where it appears | Files |
|---|---|---|
| Linux, FreeBSD | Application menu: rofi (`drun`), GNOME, KDE, wofi, fuzzel… | `~/.local/share/applications/relevo.desktop`, icon in `~/.local/share/icons/hicolor/` |
| macOS | Launchpad and Spotlight; runs in the menu bar without a Dock icon | `~/Applications/Relevo.app` |
| Windows | Start menu; the console window is detached when started from it | `Relevo.lnk` in the user's Start menu programs, icon `relevo.ico` |

Launchers do not use `PATH`, so these entries point to the binary by absolute path. Opening Relevo again while it is running only shows the running instance's panel.

### Uninstall

- Linux/FreeBSD: `rm ~/.local/bin/relevo-host ~/.local/share/applications/relevo.desktop ~/.local/share/icons/hicolor/*/apps/relevo.*`
- macOS: `rm -rf ~/.local/bin/relevo-host ~/Applications/Relevo.app`
- Windows: delete `%LOCALAPPDATA%\relevo` and the "Relevo" Start menu shortcut, and remove that folder from the user `PATH`.
- Settings live in `~/.config/relevo/` (`%APPDATA%\relevo` on Windows, `~/Library/Application Support/relevo` on macOS).

On Linux, depending on the input backend: Hyprland (`hyprland`), the `wtype` program (`wtype`), an X11 or XWayland session (`x11`), or a portal with RemoteDesktop on GNOME/KDE (`portal`). None of them needs root.

## How speakers connect

There is no Relevo server: `relevo-host` runs the relay itself and publishes it through a **tunnel** you choose when creating the session (the panel asks; tick "Remember this choice" to skip the question; in a terminal use `--tunnel`). `relevo-host backends` lists which ones are available.

| Option | Needs | HTTPS | Speaker IP | Notes (checked 2026-10-16) |
|---|---|---|---|---|
| `localhost.run` | nothing (built in) | yes | **no** | tested; **default**; no warning page |
| `serveo` | nothing (built in) | yes | yes | tested; speakers accept a warning page once |
| `cloudflared` | the `cloudflared` program | yes | yes | `trycloudflare.com` quick tunnel, no account; not tested here |
| `ngrok` | `ngrok` + authtoken | yes | yes | tested; the free plan shows a warning page (one click) |
| `tailscale` | Tailscale with Funnel | yes | — | not tested here |
| `bore` | nothing (built in) | **NO** | no | tested; plain `http://bore.pub:PORT`: password and commands unencrypted. Only with explicit consent |
| custom command | e.g. Pangolin/newt, zrok, frp | depends | — | `connection.custom = {command: [..., "{port}"], urlRegex, secure}` |
| own URL | your tunnel/proxy → `127.0.0.1:<localPort>` | depends | — | `--tunnel url:https://… --local-port 8787` |

serveo, localhost.run and bore.pub are implemented inside `relevo-host` (own SSH client and bore protocol): they need neither `ssh` nor `bore`, never use your SSH keys or agent, and pin the server key in Relevo's own `known_hosts` (accepted the first time; refused if it later changes). With providers that do not pass the speaker's IP, use **manual approval**: every speaker seems to come from the same address and the lockout after wrong passwords is capped at 10 s.

After joining, the remote tries a **direct WebRTC connection** to your computer (address discovery with public STUN: Google and Cloudflare). If it works the remote shows "direct (P2P)" and commands no longer cross the tunnel; otherwise "via tunnel". Disable it with `"connection": {"webrtc": false}`.

If the tunnel dies, Relevo restarts it; quick tunnels get a new URL and the panel tells you to **share the new link**.

## First run

```sh
relevo-host init-config            # writes ~/.config/relevo/host.json (the user config directory on each OS)
relevo-host backends               # input backends and tunnels in this binary, and what "auto" picks
relevo-host windows                # candidate target windows
relevo-host check                  # ready? (permissions, target)
relevo-host                        # tray + local panel
```

On start the **local panel** opens in the browser (`http://127.0.0.1:<port>/#t=<key>`, reachable only from your computer; the key travels in the URL fragment and is never sent to a server). From the tray: "Open panel", "Pause/Resume control", "End session", "Quit". If your desktop has no tray, Relevo detects it, opens the panel and stays in the terminal (Ctrl+C ends the session and quits). Headless: `relevo-host --tunnel localhost.run --no-tray --no-browser --start` prints the link and password.

## Language

English by default. The **EN / ES** switch in the panel changes the panel, the tray and terminal messages (stored as `"language"` in `host.json`; also `--lang es`). Each speaker picks a language in the remote with the same switch; it is remembered in their browser.

## Flow

The panel is a 4-step guide; only one step is visible at a time and the bar at the top shows where you are, what is done and what is still locked:

1. **Session**: how speakers will connect (tunnel) and options (manual approval, your own password).
2. **Target & actions**: the presentation window (saved as soon as you pick it), the application (which sets the default keys), and the buttons speakers will see. Each button shows the keys it presses; **Edit** captures another shortcut by pressing it, and **Default** goes back to the application's keys. A few shortcuts the browser keeps for itself (like Ctrl+W) cannot be captured; put those in `bindings`. "Save and create session" opens the tunnel and moves to step 3.
3. **Active session**: link and password to share, connection status, change password or end the session.
4. **Speakers & activity**: approve, disable or remove speakers and see every command. A counter on step 4 shows speakers waiting for approval.

"Pause control" stays visible at the bottom while a session runs. When the session ends the panel returns to step 1.

- Share the **link** in the call chat and the **password through another channel** (out loud, private message). "Show" and "Copy" are explicit actions.
- The display name is a label chosen by each person, not an identity.
- **Pause** stops control immediately (including anything queued). **Change password** removes every speaker. **End session** invalidates everything.

## Configuration (`host.json`)

```json
{
  "connection": { "mode": "tunnel", "tunnel": "localhost.run", "webrtc": true,
                  "stunServers": ["stun:stun.l.google.com:19302", "stun:stun.cloudflare.com:3478"] },
  "backend": "auto",
  "target": { "class": "org.pwmt.zathura", "titleContains": "", "allowUnverifiedActiveWindow": false },
  "profile": "presentation",
  "actions": ["presentation.next", "presentation.previous"],
  "bindings": { "presentation.next": ["Page_Down"] },
  "customActions": [ { "id": "custom.laser", "title": "Laser pointer", "keys": ["ctrl+l"] } ],
  "requireApproval": false,
  "language": "en",
  "adminListen": "127.0.0.1:0"
}
```

- `connection.mode`: `ask` (ask when creating the session, the default), `tunnel`, or `url` (your own tunnel, with `publicUrl` and `localPort`). Everything changes without rebuilding: the file, the panel or `--tunnel`.
- `profile`: the presentation application, which decides the default keys. Most actions use the arrow keys everywhere; starting the show differs, because F5 reloads the page in a browser:

  | Profile | Start (Windows/Linux) | Start (macOS) |
  |---|---|---|
  | `google-slides` | Ctrl+F5 | ⌘+Return |
  | `powerpoint` (desktop) | F5 | ⌘+Shift+Return |
  | `powerpoint-web` | Ctrl+F5 | ⌘+Shift+Return |
  | `keynote` | — | ⌥+⌘+P |
  | `libreoffice` | F5 | F5 |
  | `presentation`, `presentation-clicker`, `presentation-full` (generic) | F5 | F5 |

  `bindings` (what **Edit** in the panel writes) overrides any of them.
- Keys can only come from the whitelist ([platform support](platform-support.md#keys)). Speakers never send keys.
- `allowUnverifiedActiveWindow`: for backends that cannot check focus (portal; wtype outside Hyprland). With it, commands go to **whichever window has focus**. Use it only if you accept that risk.
- The password and the host token are never written to disk.

## Permissions per system

| System | What to grant |
|---|---|
| macOS | System Settings → Privacy & Security → **Accessibility** → enable `relevo-host` (or the terminal that starts it). Without it the panel says the permission is missing |
| Windows | Nothing, unless the presentation runs **as administrator**: then Windows (UIPI) blocks injection. Run the presentation without elevation. Relevo never elevates |
| Linux portal (GNOME/KDE) | Accept the system "remote control" dialog the first time; the grant is remembered with a restore token in `~/.config/relevo/portal-restore-token` |
| Linux Hyprland / wtype / X11 | Nothing |

## Troubleshooting

| Symptom | Likely cause |
|---|---|
| "Not ready: choose the presentation window" | No `target.class`/`titleContains` yet |
| Commands come back with "The presentation is not focused" | A focus-checking backend and another window has focus (correct: nothing was sent). Focus the presentation or use a targeted backend (Hyprland) |
| The tunnel does not start | Program missing or no account (ngrok: `ngrok config add-authtoken`), or the free service is down: try another one |
| Speakers cannot open the link | The tunnel restarted with a new URL (the panel says so): share the new link |
| The remote says "via tunnel" instead of "P2P" | NAT or firewall prevents a direct connection: it works the same through the tunnel, with a bit more latency |
| The session disappeared | `relevo-host` was closed or the session expired: create a new one |
| A speaker sees "Unknown result" | The confirmation was lost (network/host). The command may or may not have run; it is never retried automatically. Look at the presentation and decide |

## Known limits

- An injected key does not guarantee the application changes slide (e.g. a dialog is open).
- Checking focus and injecting is not atomic: there is a window of milliseconds.
- No video, audio, mouse or command execution: only the catalog's actions.

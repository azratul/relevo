# ADR-0015 — Built-in tunnel clients

- Status: Accepted

## Context
Running a single binary must be enough. Requiring `ssh`, `bore` or other programs to be installed breaks that.

## Decision
- **serveo.net** and **localhost.run** use a built-in SSH client (`golang.org/x/crypto/ssh`): reverse forwarding of port 80, the public URL read from the session output, `forwarded-tcpip` channels accepted directly (the services announce a different bind address than requested), keep-alives to detect dead tunnels.
- The SSH client never uses the user's keys or agent; the server's host key is pinned on first use in Relevo's own `known_hosts` (a changed key of the same type is refused).
- **bore.pub** uses a built-in implementation of the bore protocol (NUL-delimited JSON on control port 7835). It is plain TCP: the public URL is `http://`, so it is offered only with explicit consent.
- cloudflared, ngrok and Tailscale Funnel keep launching their own programs; a custom command covers other services.
- serveo is listed first (HTTPS and client IP); bore last.

## Consequences
`relevo-host` works with an empty `PATH`: verified end to end through serveo, localhost.run and bore.pub.

## Limits
These are free community services without guarantees; their output format can change (the URL is extracted with a regular expression and covered by a live test).

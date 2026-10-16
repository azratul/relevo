#!/usr/bin/env python3
"""Independent Relevo client written only from the public contract
(api/openapi.yaml, api/asyncapi.yaml, docs/protocol.md). Standard library only.

  controller: join a session, list actions, send commands, print results
              and round-trip latency percentiles.

Examples:
  relevo_client.py controller --url https://<tunnel>/s/<id> --name Ana --password xxxx-xxxx-xxxx
  relevo_client.py controller --url ... --action presentation.next --count 50 --interval 0.2
"""
import argparse, base64, getpass, json, os, socket, ssl, struct, sys, threading, time, urllib.request, urllib.error, uuid
from urllib.parse import urlparse

PROTOCOL_VERSION = "1.0"

# ---------------------------------------------------------------- WebSocket
class WebSocket:
    """Minimal RFC 6455 client: text frames, ping/pong, close."""

    def __init__(self, url, timeout=10):
        u = urlparse(url)
        port = u.port or (443 if u.scheme == "wss" else 80)
        raw = socket.create_connection((u.hostname, port), timeout=timeout)
        if u.scheme == "wss":
            raw = ssl.create_default_context().wrap_socket(raw, server_hostname=u.hostname)
        self.sock = raw
        key = base64.b64encode(os.urandom(16)).decode()
        # No Origin header: this is a native client (the credential is still required).
        req = (f"GET {u.path or '/'} HTTP/1.1\r\nHost: {u.netloc}\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n"
               f"Sec-WebSocket-Key: {key}\r\nSec-WebSocket-Version: 13\r\n\r\n")
        self.sock.sendall(req.encode())
        resp = b""
        while b"\r\n\r\n" not in resp:
            chunk = self.sock.recv(1024)
            if not chunk:
                raise ConnectionError("handshake failed")
            resp += chunk
        head, self.buf = resp.split(b"\r\n\r\n", 1)
        if b" 101 " not in head.split(b"\r\n")[0]:
            raise ConnectionError(head.split(b"\r\n")[0].decode())
        self.lock = threading.Lock()
        self.sock.settimeout(None)

    def _send_frame(self, opcode, data):
        mask = os.urandom(4)
        n = len(data)
        hdr = bytes([0x80 | opcode])
        if n < 126:
            hdr += bytes([0x80 | n])
        elif n < 65536:
            hdr += bytes([0x80 | 126]) + struct.pack("!H", n)
        else:
            hdr += bytes([0x80 | 127]) + struct.pack("!Q", n)
        masked = bytes(b ^ mask[i % 4] for i, b in enumerate(data))
        with self.lock:
            self.sock.sendall(hdr + mask + masked)

    def send(self, obj):
        self._send_frame(0x1, json.dumps(obj).encode())

    def _read(self, n):
        while len(self.buf) < n:
            chunk = self.sock.recv(65536)
            if not chunk:
                raise ConnectionError("connection closed")
            self.buf += chunk
        out, self.buf = self.buf[:n], self.buf[n:]
        return out

    def recv(self):
        """Returns a decoded JSON message, or raises ConnectionError(close code)."""
        msg = b""
        while True:
            b0, b1 = self._read(2)
            opcode, n = b0 & 0x0F, b1 & 0x7F
            if n == 126:
                n = struct.unpack("!H", self._read(2))[0]
            elif n == 127:
                n = struct.unpack("!Q", self._read(8))[0]
            data = self._read(n)  # server frames are not masked
            if opcode == 0x8:
                code = struct.unpack("!H", data[:2])[0] if len(data) >= 2 else 1005
                raise ConnectionError(f"closed by relay: {code} {data[2:].decode(errors='replace')}")
            if opcode == 0x9:
                self._send_frame(0xA, data)  # pong
                continue
            if opcode == 0xA:
                continue
            msg += data
            if b0 & 0x80:
                return json.loads(msg)

    def close(self):
        try:
            self._send_frame(0x8, struct.pack("!H", 1000))
            self.sock.close()
        except OSError:
            pass

# ---------------------------------------------------------------- helpers
def http(method, url, body=None, token=None):
    data = json.dumps(body).encode() if body is not None else None
    req = urllib.request.Request(url, data=data, method=method)
    if data is not None:
        req.add_header("Content-Type", "application/json")
    if token:
        req.add_header("Authorization", "Bearer " + token)
    try:
        with urllib.request.urlopen(req, timeout=15) as r:
            raw = r.read()
            return r.status, (json.loads(raw) if raw else None)
    except urllib.error.HTTPError as e:
        return e.code, json.loads(e.read() or b"{}")

def envelope(typ, session_id, payload):
    return {"protocolVersion": PROTOCOL_VERSION, "type": typ, "messageId": str(uuid.uuid4()),
            "sessionId": session_id, "payload": payload}

def ws_url(base):
    u = urlparse(base)
    return ("wss" if u.scheme == "https" else "ws") + "://" + u.netloc + "/api/v1/ws"

def connect(base, session_id, token, client_name):
    ws = WebSocket(ws_url(base))
    ws.send(envelope("auth.hello", session_id, {"token": token, "client": {"name": client_name, "version": "0.1.0"}}))
    first = ws.recv()
    if first["type"] != "auth.ok":
        raise SystemExit(f"auth failed: {first['payload'].get('error')}")
    return ws, first["payload"]

def pct(values, p):
    s = sorted(values)
    k = max(0, min(len(s) - 1, round(p / 100 * (len(s) - 1))))
    return s[k]

# ---------------------------------------------------------------- controller
def controller(a):
    u = urlparse(a.url)
    base = f"{u.scheme}://{u.netloc}"
    session_id = u.path.rstrip("/").split("/")[-1]
    password = a.password or getpass.getpass("Session password: ")
    status, body = http("POST", f"{base}/api/v1/sessions/{session_id}/join", {"displayName": a.name, "password": password})
    if status != 200:
        raise SystemExit(f"join failed ({status}): {body['error']['code']} {body['error']['message']}")
    token = body["token"]
    print(f"joined as {body['participantId']} state={body['state']}")
    ws, ok = connect(base, session_id, token, "python-example")
    view = ws.recv()["payload"]  # session.snapshot
    catalog = view["catalog"]
    if not catalog:
        print("host offline: no catalog")
    else:
        print(f"catalog revision {catalog['revision']}:")
        for act in catalog["actions"]:
            print(f"  {act['id']:<26} {act['title']} (needs {act['capability']}, confirmation {act['confirmation']})")
    seq = 0
    latencies = []
    pending = {}
    results = {}
    done = threading.Event()

    def reader():
        nonlocal view
        try:
            while True:
                m = ws.recv()
                t, p = m["type"], m["payload"]
                if t in ("session.snapshot", "session.updated"):
                    view = p
                elif t == "command.accepted":
                    pending[p["commandId"]]["accepted"] = time.monotonic()
                elif t in ("command.result", "command.rejected"):
                    cid = p["commandId"]
                    st = p.get("status", "rejected")
                    rec = pending.get(cid)
                    if rec:
                        rec["end"] = time.monotonic()
                        results[cid] = (st, p.get("confirmation"), (p.get("error") or {}).get("code"))
                        if st == "executed":
                            latencies.append((rec["end"] - rec["start"]) * 1000)
                        if not a.quiet:
                            print(f"  #{p.get('sequence', '-')} {rec['action']}: {st} {p.get('confirmation') or (p.get('error') or {}).get('code', '')}"
                                  f" ({(rec['end'] - rec['start']) * 1000:.1f} ms)")
                elif t == "session.closed":
                    print("session closed:", p["reason"])
                    break
                elif t == "error":
                    print("relay error:", p["error"])
        except ConnectionError as e:
            print(e)
        done.set()

    threading.Thread(target=reader, daemon=True).start()

    def send(action, params):
        nonlocal seq
        seq += 1
        cid = str(uuid.uuid4())
        pending[cid] = {"action": action, "start": time.monotonic()}
        ws.send(envelope("command.request", session_id, {"commandId": cid, "clientSequence": seq, "action": action,
                                                        "parameters": params, "catalogRevision": view["catalog"]["revision"]}))
        return cid

    if a.action:
        params = json.loads(a.params)
        for _ in range(a.count):
            send(a.action, params)
            time.sleep(a.interval)
        deadline = time.monotonic() + 10
        while len(results) < a.count and time.monotonic() < deadline and not done.is_set():
            time.sleep(0.05)
        counts = {}
        for st, _, code in results.values():
            counts[st if not code else f"{st}:{code}"] = counts.get(st if not code else f"{st}:{code}", 0) + 1
        print("results:", counts, f"(missing {a.count - len(results)})")
        if latencies:
            print(f"round trip until result (ms): n={len(latencies)} p50={pct(latencies, 50):.1f} "
                  f"p90={pct(latencies, 90):.1f} p99={pct(latencies, 99):.1f} max={max(latencies):.1f}")
    else:
        print("type an action id (empty line = presentation.next, q = quit)")
        for line in sys.stdin:
            line = line.strip()
            if line == "q":
                break
            send(line or "presentation.next", {})
    http("POST", f"{base}/api/v1/sessions/{session_id}/leave", None, token)
    ws.close()

def main():
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    sub = ap.add_subparsers(dest="cmd", required=True)
    c = sub.add_parser("controller")
    c.add_argument("--url", required=True, help="join link https://relay/s/<sessionId>")
    c.add_argument("--name", default="python")
    c.add_argument("--password", help="omit to be prompted (avoid passwords in shell history)")
    c.add_argument("--action")
    c.add_argument("--params", default="{}")
    c.add_argument("--count", type=int, default=1)
    c.add_argument("--interval", type=float, default=0.3)
    c.add_argument("--quiet", action="store_true")
    a = ap.parse_args()
    controller(a)

if __name__ == "__main__":
    main()

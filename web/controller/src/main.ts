import "./style.css";
import { ActionDescriptor, Envelope, HttpError, Participant, ProtoError, SessionView, envelope, join, leave, uuid, wsURL } from "./protocol";
import { shortcutFor } from "./keys";
import { Lang, M, Msg, has, lang, msg, raw, setLang, t } from "./i18n";

// ---------- small DOM helpers (untrusted text only via textContent) ----------
type Child = Node | string | null | undefined | false;
function h<K extends keyof HTMLElementTagNameMap>(tag: K, attrs: Record<string, any> = {}, ...children: Child[]): HTMLElementTagNameMap[K] {
  const e = document.createElement(tag);
  for (const [k, v] of Object.entries(attrs)) {
    if (v === undefined || v === null || v === false) continue;
    if (k.startsWith("on")) (e as any)[k] = v;
    else if (k === "class") e.className = v;
    else e.setAttribute(k, v === true ? "" : String(v));
  }
  for (const c of children) if (c !== null && c !== undefined && c !== false) e.append(c);
  return e;
}
const app = document.getElementById("app")!;

// ---------- messages ----------
const errText = (e?: ProtoError): Msg | null => (!e ? null : has("err." + e.code) ? M("err." + e.code) : raw(e.message));
const actionTitle = (a: { id: string; title: string }) => (has("action." + a.id) ? t("action." + a.id) : a.title);
const reasonText = (r?: string) => (r && has("reason." + r) ? t("reason." + r) : r ?? "");

// ---------- state ----------
type CmdState = "sending" | "accepted" | "executed" | "rejected" | "failed" | "expired" | "unknown" | "lost";
interface Cmd { id: string; action: string; title: string; state: CmdState; detail: Msg | null; at: number; msgId: string }
type Conn = "connecting" | "online" | "reconnecting" | "offline";

const sessionId = (location.pathname.match(/^\/s\/([A-Za-z0-9_-]{22})\/?$/) || [])[1] ?? null;
let token: string | null = null; // memory only (ADR-0003): reload = login again
let me: Participant | null = null;
let view: SessionView | null = null;
let conn: Conn = "offline";
let clientSequence = 0;
let closedReason: Msg | null = null;
let notice: Msg | null = null;
const cmds: Cmd[] = [];
let heartbeat = { intervalMs: 15000, timeoutMs: 30000 };
let lastPong = 0;
let pingTimer: number | undefined;
let retry = 0;
let retryTimer: number | undefined;

// The same protocol runs over two transports (ADR-0014): the WebSocket
// through the host's tunnel, and, when possible, a direct WebRTC DataChannel.
type Transport = "websocket" | "webrtc";
interface Chan { kind: Transport; send(s: string): void; close(): void }
let chan: Chan | null = null;          // authenticated channel used for commands
let ws: WebSocket | null = null;
let rtc: { pc: RTCPeerConnection; dc: RTCDataChannel } | null = null;
let upgrading = false;                  // a DataChannel auth is in flight: the relay will close the WebSocket (4009)
let lastUpgradeTry = 0;

function connect() {
  if (!token || !sessionId) return;
  conn = retry === 0 ? "connecting" : "reconnecting";
  render();
  const sock = new WebSocket(wsURL());
  ws = sock;
  let authed = false;
  const wsChan: Chan = { kind: "websocket", send: (s) => sock.send(s), close: () => sock.close() };
  sock.onopen = () => sock.send(JSON.stringify(envelope("auth.hello", sessionId, { token, client: { name: "relevo-web", version: "0.1.0" } })));
  sock.onmessage = (ev) => {
    if (ws !== sock) return;
    let m: Envelope;
    try { m = JSON.parse(ev.data); } catch { return; }
    if (m.type === "auth.ok") authed = true;
    handle(m, wsChan);
  };
  sock.onclose = (ev) => {
    if (ws !== sock) return;
    ws = null;
    // Superseded by our own DataChannel: expected, not an error.
    if (ev.code === 4009 && (upgrading || chan?.kind === "webrtc")) return;
    if (chan === wsChan) { chan = null; window.clearInterval(pingTimer); }
    if (closedReason || ev.code === 4003 || ev.code === 4004) { conn = "offline"; closedReason ??= M("closed.default"); token = null; closeRTC(); render(); return; }
    if (ev.code === 4009) { conn = "offline"; notice = M("conn.superseded"); render(); return; }
    if (ev.code === 4002) { conn = "offline"; notice = M("err.UNSUPPORTED_VERSION"); render(); return; }
    if (ev.code === 4001 && !authed) { logout(M("conn.authFailed")); return; }
    if (chan) return; // the DataChannel is still carrying the session
    scheduleReconnect();
  };
}

// Commands not yet answered stay as they are; after reconnecting we ask for
// their status. We never resend them (ADR-0006).
function scheduleReconnect() {
  conn = "reconnecting";
  const delay = Math.min(15000, 500 * 2 ** retry) * (0.5 + Math.random() / 2);
  retry++;
  window.clearTimeout(retryTimer);
  retryTimer = window.setTimeout(connect, delay);
  render();
}

function handle(m: Envelope, from: Chan) {
  switch (m.type) {
    case "auth.ok":
      retry = 0; conn = "online";
      heartbeat = m.payload.heartbeat ?? heartbeat;
      chan = from;
      if (from.kind === "webrtc") upgrading = false;
      startPing();
      resolvePending();
      if (from.kind === "websocket" && m.payload.rtc && (m.payload.capabilities ?? []).includes("webrtc")) void tryUpgrade(m.payload.rtc.iceServers);
      break;
    case "auth.error":
      if (from.kind === "webrtc") { upgrading = false; closeRTC(); return; }
      if (["AUTH_FAILED", "AUTH_EXPIRED"].includes(m.payload.error.code)) { logout(errText(m.payload.error)); return; }
      notice = errText(m.payload.error);
      break;
    case "session.snapshot": case "session.updated":
      view = m.payload; if (view?.you) me = view.you;
      break;
    case "participant.updated":
      me = m.payload.participant;
      break;
    case "session.closed":
      closedReason = has("closed." + m.payload.reason) ? M("closed." + m.payload.reason) : M("closed.default");
      token = null; conn = "offline"; closeRTC(); ws?.close();
      break;
    case "command.accepted": update(m.payload.commandId, "accepted", null); break;
    case "command.rejected": update(m.payload.commandId, "rejected", errText(m.payload.error)); break;
    case "command.result": {
      const p = m.payload;
      update(p.commandId, p.status, p.status === "executed" ? confirmText(p.confirmation) : errText(p.error));
      break;
    }
    case "command.status": {
      const p = m.payload;
      if (!p.found) update(p.commandId, "lost", M("cmd.lost"));
      else if (p.status === "accepted" || p.status === "dispatched") update(p.commandId, "accepted", null);
      else update(p.commandId, p.status, p.status === "executed" ? confirmText(p.confirmation) : errText(p.error));
      break;
    }
    case "pong": lastPong = Date.now(); return; // nothing visible changed
    case "error": notice = errText(m.payload.error); break;
  }
  render();
}

// tryUpgrade opens a direct DataChannel to the host (STUN only, no TURN).
// If ICE cannot connect (strict NAT), everything simply stays on the tunnel.
async function tryUpgrade(iceServers: RTCIceServer[]) {
  if (upgrading || rtc || typeof RTCPeerConnection === "undefined" || Date.now() - lastUpgradeTry < 30000) return;
  lastUpgradeTry = Date.now();
  const tok = token, sid = sessionId!;
  const pc = new RTCPeerConnection({ iceServers });
  try {
    const dc = pc.createDataChannel("relevo", { ordered: true });
    await pc.setLocalDescription(await pc.createOffer());
    await waitFor((done) => { if (pc.iceGatheringState === "complete") done(); pc.onicegatheringstatechange = () => pc.iceGatheringState === "complete" && done(); }, 2500);
    const res = await fetch(`/api/v1/sessions/${encodeURIComponent(sid)}/rtc`, {
      method: "POST", headers: { "Content-Type": "application/json", Authorization: "Bearer " + tok },
      body: JSON.stringify({ type: "offer", sdp: pc.localDescription!.sdp }), cache: "no-store",
    });
    if (!res.ok) throw new Error("rtc " + res.status);
    await pc.setRemoteDescription(await res.json());
    const opened = await waitFor((done) => { if (dc.readyState === "open") done(); dc.onopen = () => done(); }, 8000);
    if (!opened || token !== tok) throw new Error("datachannel did not open");
    rtc = { pc, dc };
    const rtcChan: Chan = { kind: "webrtc", send: (s) => dc.send(s), close: () => closeRTC() };
    dc.onmessage = (ev) => { try { handle(JSON.parse(ev.data), rtcChan); } catch { /* ignore */ } };
    const lost = () => {
      if (rtc?.dc !== dc) return;
      closeRTC();
      if (chan === rtcChan) { chan = null; window.clearInterval(pingTimer); if (token && !closedReason) { retry = 0; scheduleReconnect(); } }
    };
    dc.onclose = lost;
    pc.onconnectionstatechange = () => { if (["failed", "closed", "disconnected"].includes(pc.connectionState)) lost(); };
    upgrading = true;
    dc.send(JSON.stringify(envelope("auth.hello", sid, { token: tok, client: { name: "relevo-web", version: "0.1.0" } })));
    window.setTimeout(() => { if (upgrading) { upgrading = false; closeRTC(); } }, 5000);
  } catch {
    pc.close();
    if (rtc?.pc === pc) rtc = null;
    upgrading = false;
  }
}

function waitFor(setup: (done: () => void) => void, ms: number): Promise<boolean> {
  return new Promise((resolve) => {
    let finished = false;
    const done = () => { if (!finished) { finished = true; resolve(true); } };
    setup(done);
    window.setTimeout(() => { if (!finished) { finished = true; resolve(false); } }, ms);
  });
}

function closeRTC() {
  const r = rtc; rtc = null;
  if (r) { r.dc.onclose = null; r.pc.onconnectionstatechange = null; r.dc.close(); r.pc.close(); }
}

function sendRaw(s: string) { chan?.send(s); }

function startPing() {
  lastPong = Date.now();
  window.clearInterval(pingTimer);
  pingTimer = window.setInterval(() => {
    if (Date.now() - lastPong > heartbeat.timeoutMs) { chan?.close(); return; } // stalled link
    sendRaw(JSON.stringify({ protocolVersion: "1.0", type: "ping", messageId: uuid(), payload: {} }));
  }, Math.max(2000, Math.min(heartbeat.intervalMs, 15000)));
}

function resolvePending() {
  for (const c of cmds) if (c.state === "sending" || c.state === "accepted") sendRaw(JSON.stringify(envelope("command.status.request", sessionId!, { commandId: c.id })));
}

function confirmText(c?: string): Msg | null {
  return c && has("confirm." + c) ? M("confirm." + c) : null;
}

function update(id: string, state: CmdState, detail: Msg | null) {
  const c = cmds.find((x) => x.id === id);
  if (!c) return;
  const terminal = ["executed", "rejected", "failed", "expired", "unknown", "lost"];
  if (terminal.includes(c.state) && state === "accepted") return;
  c.state = state; c.detail = detail;
}

// ---------- commands ----------
function send(a: ActionDescriptor, parameters: Record<string, unknown> = {}) {
  if (!chan || conn !== "online" || !view?.catalog || !sessionId) return;
  clientSequence++;
  const id = uuid();
  const env = envelope("command.request", sessionId, { commandId: id, clientSequence, action: a.id, parameters, catalogRevision: view.catalog.revision });
  cmds.unshift({ id, action: a.id, title: a.title, state: "sending", detail: null, at: Date.now(), msgId: env.messageId });
  cmds.length = Math.min(cmds.length, 8);
  sendRaw(JSON.stringify(env));
  render();
}

function actions(): ActionDescriptor[] { return view?.catalog?.actions ?? []; }
function canControl(a?: ActionDescriptor): { ok: boolean; why: string } {
  if (conn !== "online") return { ok: false, why: t("why.offline") };
  if (!me) return { ok: false, why: "" };
  if (me.state === "pending") return { ok: false, why: t("why.pending") };
  if (me.state !== "enabled") return { ok: false, why: t("why.disabled") };
  if (!view?.host.online) return { ok: false, why: t("err.HOST_OFFLINE") };
  if (!view.host.ready) return { ok: false, why: view.host.readinessReason ? t("why.notReadyR", { r: reasonText(view.host.readinessReason) }) : t("why.notReady") };
  if (view.paused) return { ok: false, why: t("err.SESSION_PAUSED") };
  if (a && !me.capabilities.includes(a.capability)) return { ok: false, why: t("err.PERMISSION_DENIED") };
  return { ok: true, why: "" };
}

function logout(m: Msg | null) {
  window.clearInterval(pingTimer); window.clearTimeout(retryTimer);
  const s = ws; ws = null; s?.close(); closeRTC(); chan = null; upgrading = false;
  token = null; me = null; view = null; conn = "offline"; notice = m; cmds.length = 0; clientSequence = 0;
  render();
}

// ---------- views ----------
// The remote screen is built once and then updated in place: replacing DOM
// nodes on every message (pongs included) could swallow a click whose
// keydown/keyup straddle the update, and would clear half-typed inputs.
type Screen = "none" | "closed" | "login" | "remote";
let screen: Screen | null = null;
let rv: RemoteView | null = null;

function render() {
  const next: Screen = !sessionId ? "none" : closedReason ? "closed" : !token ? "login" : "remote";
  if (next === "remote") {
    if (screen !== "remote" || !rv) { rv = buildRemote(); app.replaceChildren(rv.root); rv.extraRev = ""; }
    updateRemote(rv);
  } else {
    const focus = document.activeElement?.id;
    rv = null;
    app.replaceChildren(next === "none" ? noSession() : next === "closed" ? closed() : login());
    if (focus) document.getElementById(focus)?.focus();
  }
  screen = next;
}

function noSession() {
  const input = h("input", { id: "link", type: "url", placeholder: "https://…/s/…", autocomplete: "off", required: true });
  return h("main", { class: "narrow" },
    h("h1", {}, "Relevo"),
    langSwitch(),
    h("p", {}, t("noSession.lead")),
    h("form", { onsubmit: (e: Event) => { e.preventDefault(); try { const u = new URL(input.value); if (u.origin === location.origin) location.href = u.pathname; else notice = M("noSession.otherServer"); } catch { notice = M("noSession.invalid"); } render(); } },
      h("label", { for: "link" }, t("noSession.link")), input, h("button", { class: "primary", type: "submit" }, t("noSession.open"))),
    notice && h("p", { class: "error", role: "alert" }, msg(notice)));
}

function closed() {
  return h("main", { class: "narrow" }, langSwitch(), h("h1", {}, t("closed.title")), h("p", { role: "status" }, msg(closedReason)),
    h("button", { class: "primary", onclick: () => { closedReason = null; notice = null; render(); } }, t("closed.rejoin")));
}

let busy = false;
let typedName: string | null = null;
function login() {
  const name = h("input", { id: "name", autocomplete: "nickname", maxlength: 40, required: true, value: typedName ?? localStorageGet("relevo.name") ?? "",
    oninput: (e: Event) => (typedName = (e.target as HTMLInputElement).value) });
  const pw = h("input", { id: "password", type: "password", autocomplete: "off", required: true, maxlength: 128 });
  const form = h("form", {
    onsubmit: async (e: Event) => {
      e.preventDefault();
      if (busy) return;
      busy = true; notice = null; render();
      try {
        const res = await join(sessionId!, name.value.trim(), pw.value);
        localStorageSet("relevo.name", name.value.trim());
        token = res.token; clientSequence = 0; retry = 0;
        connect();
      } catch (err) {
        notice = err instanceof HttpError
          ? (err.error.code === "RATE_LIMITED" && err.error.retryAfterMs ? M("login.wait", { s: Math.ceil(err.error.retryAfterMs / 1000) }) : errText(err.error))
          : M("login.network");
      } finally { busy = false; render(); }
    },
  },
    h("label", { for: "name" }, t("login.name")), name,
    h("p", { class: "hint" }, t("login.nameHint")),
    h("label", { for: "password" }, t("login.password")), pw,
    h("button", { class: "primary", type: "submit", disabled: busy }, busy ? t("login.joining") : t("login.submit")));
  return h("main", { class: "narrow" }, langSwitch(), h("h1", {}, t("login.title")), form, notice && h("p", { class: "error", role: "alert" }, msg(notice)));
}


interface RemoteView {
  root: HTMLElement; conn: HTMLElement; host: HTMLElement; who: HTMLElement; why: HTMLElement; notice: HTMLElement; noticeText: HTMLElement;
  prev: HTMLButtonElement; next: HTMLButtonElement; extra: HTMLElement; extraRev: string; extraButtons: Map<string, HTMLButtonElement>;
  log: HTMLOListElement; target: HTMLElement;
}

function buildRemote(): RemoteView {
  const big = (id: string, cls: string, label: string, action: string) =>
    h("button", { id, class: "big " + cls, onclick: () => { const a = actions().find((x) => x.id === action); if (a && canControl(a).ok) send(a); } }, label) as HTMLButtonElement;
  const noticeText = h("span");
  const v: RemoteView = {
    root: h("main", { class: "remote" }),
    conn: h("span", { class: "pill", role: "status" }), host: h("span", { class: "pill" }), who: h("span", { class: "who" }),
    why: h("p", { class: "why", role: "status" }),
    noticeText,
    notice: h("p", { class: "error", role: "alert" }, noticeText, " ", h("button", { class: "link", onclick: () => { notice = null; render(); } }, t("close"))),
    prev: big("btn-prev", "prev", t("btn.prev"), "presentation.previous"),
    next: big("btn-next", "next", t("btn.next"), "presentation.next"),
    extra: h("div", { class: "extra" }), extraRev: "", extraButtons: new Map(),
    log: h("ol", { class: "log", "aria-live": "polite" }), target: h("p", { class: "hint" }),
  };
  v.root.append(
    h("header", { class: "bar" }, v.conn, v.host, v.who, langSwitch(),
      h("button", { class: "link", onclick: () => { if (token && sessionId) leave(sessionId, token); logout(M("left")); } }, t("leave"))),
    v.why, v.notice, h("div", { class: "pad" }, v.prev, v.next), v.extra,
    h("p", { class: "hint keys" }, t("keys")),
    h("section", { "aria-label": t("log") }, v.log), v.target);
  return v;
}

function setText(e: HTMLElement, t: string) { if (e.textContent !== t) e.textContent = t; }

function updateRemote(v: RemoteView) {
  const ctl = canControl();
  setText(v.conn, conn === "online" ? (chan?.kind === "webrtc" ? t("pill.p2p") : t("pill.tunnel"))
    : { connecting: t("pill.connecting"), online: "", reconnecting: t("pill.reconnecting"), offline: t("pill.offline") }[conn]);
  v.conn.dataset.transport = conn === "online" ? chan?.kind ?? "" : "";
  v.conn.className = "pill " + (conn === "online" ? "ok" : "warn");
  const hostLabel = !view ? "" : !view.host.online ? t("host.offline") : view.paused ? t("host.paused") : view.host.ready ? t("host.ready") : t("host.notReady");
  setText(v.host, hostLabel); v.host.hidden = !hostLabel;
  v.host.className = "pill " + (view?.host.online && view.host.ready && !view.paused ? "ok" : "warn");
  setText(v.who, me ? me.displayName + (me.state === "enabled" ? "" : " · " + t("me." + me.state)) : "");
  setText(v.why, ctl.why); v.why.hidden = ctl.ok || !ctl.why;
  setText(v.noticeText, msg(notice)); v.notice.hidden = !notice;
  for (const [btn, id] of [[v.prev, "presentation.previous"], [v.next, "presentation.next"]] as const) {
    const a = actions().find((x) => x.id === id);
    btn.hidden = !a;
    btn.disabled = !a || !canControl(a).ok;
    if (a) { btn.setAttribute("aria-label", actionTitle(a)); btn.title = actionTitle(a); }
  }
  // Extra controls are rebuilt only when the catalog changes.
  const extra = actions().filter((a) => a.id !== "presentation.previous" && a.id !== "presentation.next");
  const rev = String(view?.catalog?.revision ?? "") + ":" + extra.map((a) => a.id).join(",");
  if (rev !== v.extraRev) {
    v.extraRev = rev; v.extraButtons.clear();
    v.extra.replaceChildren(...extra.map((a) => extraControl(a, v.extraButtons)));
  }
  v.extra.hidden = extra.length === 0;
  for (const a of extra) { const b = v.extraButtons.get(a.id); if (b) b.disabled = !canControl(a).ok; }
  v.log.replaceChildren(...cmds.map((c) =>
    h("li", { class: "st-" + c.state }, h("strong", {}, actionTitle({ id: c.action, title: c.title })), " — ", t("state." + c.state), c.detail && h("span", { class: "detail" }, " · " + msg(c.detail)))));
  const tg = view?.host.target;
  setText(v.target, tg ? t("target", { l: tg.label }) + (tg.focusCheck === "unverified" ? t("target.unverified") : "") : "");
}

function extraControl(a: ActionDescriptor, buttons: Map<string, HTMLButtonElement>) {
  const props = a.parameters?.properties ?? {};
  const keys = Object.keys(props);
  const current = () => actions().find((x) => x.id === a.id);
  if (keys.length === 0) {
    const b = h("button", { class: "secondary", onclick: () => { const x = current(); if (x && canControl(x).ok) send(x); } }, actionTitle(a)) as HTMLButtonElement;
    buttons.set(a.id, b);
    return b;
  }
  // Simple form for integer parameters (e.g. presentation.goto).
  const inputs = keys.map((k) => h("input", { id: "p-" + a.id + "-" + k, type: "number", inputmode: "numeric", min: props[k].minimum, max: props[k].maximum, required: (a.parameters.required ?? []).includes(k), "aria-label": has("param." + k) ? t("param." + k) : props[k].title ?? k, placeholder: has("param." + k) ? t("param." + k) : props[k].title ?? k }));
  const b = h("button", { class: "secondary", type: "submit" }, actionTitle(a)) as HTMLButtonElement;
  buttons.set(a.id, b);
  return h("form", { class: "param", onsubmit: (e: Event) => {
    e.preventDefault();
    const x = current();
    if (!x || !canControl(x).ok) return;
    const params: Record<string, unknown> = {};
    keys.forEach((k, i) => { if (inputs[i].value !== "") params[k] = props[k].type === "integer" ? parseInt(inputs[i].value, 10) : inputs[i].value; });
    send(x, params);
    inputs.forEach((i) => (i.value = ""));
  } }, ...inputs, b);
}

function langSwitch() {
  const mk = (l: Lang) => h("button", { type: "button", class: "lang-btn", "aria-pressed": String(lang === l), onclick: () => { setLang(l); rv = null; screen = null; render(); } }, l.toUpperCase());
  return h("span", { class: "lang", role: "group", "aria-label": "Language" }, mk("en"), mk("es"));
}

function localStorageGet(k: string) { try { return localStorage.getItem(k); } catch { return null; } }
function localStorageSet(k: string, v: string) { try { localStorage.setItem(k, v); } catch { /* private mode */ } }

// ---------- keyboard ----------
document.addEventListener("keydown", (e) => {
  if (!token || closedReason) return;
  const s = shortcutFor(e);
  if (!s) return;
  const a = actions().find((x) => x.id === s);
  if (!a) return;
  e.preventDefault();
  if (canControl(a).ok) send(a);
});
window.addEventListener("pagehide", () => { if (token && sessionId) leave(sessionId, token); });

render();

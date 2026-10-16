// Local host panel: a 4-step guide (session → target → share → speakers).
// Plain JS; untrusted text (names, titles) only via textContent.
const token = (() => {
  const m = location.hash.match(/t=([A-Za-z0-9_-]+)/);
  if (m) { sessionStorage.setItem("t", m[1]); history.replaceState(null, "", location.pathname); return m[1]; }
  return sessionStorage.getItem("t") || "";
})();
const $ = (id) => document.getElementById(id);
const el = (tag, text, cls) => { const e = document.createElement(tag); if (text !== undefined) e.textContent = text; if (cls) e.className = cls; return e; };

// ---------- i18n (English default) ----------
const I18N = {
  en: {
    host: "host", step1: "Session", step2: "Target & actions", step3: "Active session", step4: "Speakers & activity",
    "s1.title": "How will speakers connect?",
    "s1.lead": "There is no Relevo server: your computer is the server and a tunnel makes it reachable. If the speaker's browser can reach you directly (WebRTC), commands go peer-to-peer; otherwise they keep using the tunnel.",
    "s1.locked": "A session is running. End it (step 3) to change these options.",
    "s1.ownUrl": "Public URL of your tunnel or proxy", "s1.localPort": "Local port",
    "s1.insecure": "I understand this tunnel does NOT encrypt: the password and commands travel in clear text.",
    "s1.remember": "Remember this choice", "s1.more": "More options", "s1.approval": "Approve each speaker manually",
    "s1.customPw": "Own password (optional, at least 12 characters; empty = generated)",
    "s1.next": "Next: choose the presentation →", "s1.ended": "Last session ended: {r}",
    "s1.recommended": "recommended", "s1.builtin": "built in", "s1.notInstalled": "not installed", "s1.account": "account: {a}",
    "mode.url": "My own tunnel or proxy (Pangolin, frp, …)", "prov.url": "Own URL", "mode.url.note": "Your tunnel forwards its public URL to a fixed local port",
    "tunnel.serveo": "HTTPS; speakers accept a warning page the first time",
    "tunnel.localhost.run": "HTTPS; does not pass the speakers' IP (use manual approval)",
    "tunnel.cloudflared": "trycloudflare.com: no account; needs the cloudflared program",
    "tunnel.ngrok": "Needs the ngrok program and an account; the free plan shows a warning page",
    "tunnel.tailscale": "Needs Tailscale with Funnel enabled",
    "tunnel.bore": "Plain http://: password and commands travel UNENCRYPTED",
    "s2.title": "Which window is the presentation?",
    "s2.lead": "Open your presentation, then pick its window here. Relevo only sends the allowed keys to that window.",
    "s2.window": "Presentation window", "s2.pick": "— choose —", "s2.refresh": "Refresh list",
    "s2.unverified": "Send to the active window without checking focus (risk: keys may reach another application)",
    "s2.profile": "Application (default keys)",
    "s2.keysHint": "Each button presses these keys in the presentation window on this computer. Use Edit if your application or system uses another shortcut.",
    "s2.edit": "Edit", "s2.editAria": "Edit keys for {a}", "s2.press": "Press the shortcut…", "s2.saveKey": "Save", "s2.defaultKey": "Default ({k})", "s2.cancel": "Cancel",
    "s2.customKey": "custom", "s2.keyBad": "That key is not allowed", "s2.gotoKeys": "number + Enter",
    "s2.reserved": "The browser keeps some shortcuts for itself (like Ctrl+W); set those in host.json.", "s2.actions": "Buttons speakers will see", "s2.advanced": "Advanced", "s2.backend": "Input method",
    "s2.class": "Window class / app_id", "s2.title2": "Title contains", "s2.create": "Save and create session ▶",
    "s2.saveContinue": "Save and continue →", "s2.creating": "Opening the tunnel…",
    "s2.ready": "✔ Ready: {t}", "s2.notReady": "✖ Not ready: {r}", "s2.confirmNotReady": "The presentation window is not ready ({r}). Create the session anyway?",
    "s2.perm": "System permission needed: {p}", "s2.noWindows": "No windows found",
    "s3.title": "Share the session", "s3.h1": "Paste the link in the video call chat.",
    "s3.h2": "Tell the password separately (out loud or in a private message).", "s3.h3": "Speakers open the link, type their name and the password.",
    "s3.link": "Link", "s3.password": "Password", "s3.manage": "Manage session", "s3.rotate": "Change password (removes everyone)",
    "s3.end": "End session", "s3.back": "← Target", "s3.next": "Next: speakers →", "s3.expires": "Expires: {d}",
 "s3.conn": "{n} · {s} · WebRTC peer-to-peer {w}", "s3.https": "HTTPS", "s3.plain": "UNENCRYPTED",
    "s3.on": "on", "s3.off": "off", "s3.noIp": " · the tunnel does not pass speakers' IPs (manual approval recommended)",
    "s3.confirmEnd": "End the session? All speakers will lose access.", "s3.confirmRotate": "A new password will be generated and every speaker removed. Continue?",
    "s4.title": "Speakers", "s4.lead": "Names are labels chosen by each person, not verified identities.",
    "s4.name": "Name", "s4.state": "State", "s4.conn": "Connection", "s4.extra": "Extra permissions", "s4.activity": "Activity",
    "s4.back": "← Session", "s4.none": "Nobody yet. Share the link and password (step 3).", "s4.noActivity": "No commands yet.",
    "s4.online": "online", "s4.offline": "offline", "s4.approve": "Approve", "s4.disable": "Disable", "s4.enable": "Enable", "s4.revoke": "Remove",
    "s4.allow": "Allow {a} for {n}",
    "state.pending": "waiting for approval", "state.enabled": "enabled", "state.disabled": "disabled", "state.revoked": "removed",
    "hist.executed": "executed", "hist.rejected": "rejected", "hist.failed": "failed", "hist.expired": "expired", "hist.dropped": "dropped",
    "chip.none": "No session", "chip.online": "Online", "chip.reconnecting": "Reconnecting…", "chip.paused": "Paused", "chip.ready": "Target ready", "chip.notReady": "Target not ready",
    pause: "Pause control", resume: "Resume control", quit: "Quit Relevo", back: "← Back", copy: "Copy", copied: "Copied", show: "Show", hide: "Hide",
    "quit.confirm": "Quit? The active session will end.", "quit.done": "Relevo has been closed.",
    "err.chooseMode": "Choose how speakers will connect", "err.insecure": "Confirm that you accept an unencrypted tunnel, or choose another",
    "err.panel": "Panel disconnected",
    "reason.no_target": "choose the presentation window", "reason.target_not_found": "the presentation window is not open",
    "reason.unverified_optin": "this input method cannot check the focused window; enable \"send without checking\" or choose another",
    "reason.accessibility_permission": "grant the Accessibility permission to relevo-host (System Settings → Privacy & Security → Accessibility)",
    "reason.no_focus_info": "the window manager does not report the focused window",
    "warn.tunnel_down": "The tunnel went down; reconnecting…", "warn.tunnel_failed": "Could not reopen the tunnel; retrying…", "warn.link_changed": "The link changed: share the new one",
    "profile.presentation": "Other (arrow keys, F5 to start)", "profile.presentation-clicker": "Other (Page Up / Page Down, like a clicker)", "profile.presentation-full": "Other (all actions)",
    "profile.google-slides": "Google Slides", "profile.powerpoint": "PowerPoint (desktop)", "profile.powerpoint-web": "PowerPoint for the web", "profile.keynote": "Keynote (macOS)", "profile.libreoffice": "LibreOffice Impress",
    "action.presentation.next": "Next", "action.presentation.previous": "Previous", "action.presentation.first": "First slide", "action.presentation.last": "Last slide",
    "action.presentation.blank": "Black screen", "action.presentation.goto": "Go to slide", "action.presentation.start": "Start presentation", "action.presentation.stop": "Exit presentation",
  },
  es: {
    host: "anfitrión", step1: "Sesión", step2: "Destino y acciones", step3: "Sesión activa", step4: "Relatores y actividad",
    "s1.title": "¿Cómo se conectarán los relatores?",
    "s1.lead": "No hay servidor de Relevo: tu equipo hace de servidor y un túnel lo hace accesible. Si el navegador del relator puede llegar directamente a ti (WebRTC), las órdenes van P2P; si no, siguen por el túnel.",
    "s1.locked": "Hay una sesión en curso. Termínala (paso 3) para cambiar estas opciones.",
    "s1.ownUrl": "URL pública de tu túnel o proxy", "s1.localPort": "Puerto local",
    "s1.insecure": "Entiendo que este túnel NO cifra: la contraseña y las órdenes viajan en claro.",
    "s1.remember": "Recordar esta elección", "s1.more": "Más opciones", "s1.approval": "Aprobar manualmente a cada relator",
    "s1.customPw": "Contraseña propia (opcional, al menos 12 caracteres; vacío = generada)",
    "s1.next": "Siguiente: elegir la presentación →", "s1.ended": "Última sesión terminada: {r}",
    "s1.recommended": "recomendado", "s1.builtin": "integrado", "s1.notInstalled": "no instalado", "s1.account": "cuenta: {a}",
    "mode.url": "Mi propio túnel o proxy (Pangolin, frp, …)", "prov.url": "URL propia", "mode.url.note": "Tu túnel reenvía su URL pública a un puerto local fijo",
    "tunnel.serveo": "HTTPS; el relator acepta una página de aviso la primera vez",
    "tunnel.localhost.run": "HTTPS; no informa de la IP de los relatores (usa aprobación manual)",
    "tunnel.cloudflared": "trycloudflare.com: sin cuenta; necesita el programa cloudflared",
    "tunnel.ngrok": "Necesita el programa ngrok y una cuenta; el plan gratuito muestra una página de aviso",
    "tunnel.tailscale": "Necesita Tailscale con Funnel activado",
    "tunnel.bore": "http:// en claro: la contraseña y las órdenes viajan SIN CIFRAR",
    "s2.title": "¿Qué ventana es la presentación?",
    "s2.lead": "Abre tu presentación y elige aquí su ventana. Relevo solo envía las teclas permitidas a esa ventana.",
    "s2.window": "Ventana de la presentación", "s2.pick": "— elegir —", "s2.refresh": "Actualizar lista",
    "s2.unverified": "Enviar a la ventana activa sin verificar el foco (riesgo: las teclas pueden llegar a otra aplicación)",
    "s2.profile": "Aplicación (teclas predeterminadas)",
    "s2.keysHint": "Cada botón pulsa estas teclas en la ventana de la presentación en este computador. Usa Editar si tu aplicación o sistema usa otro atajo.",
    "s2.edit": "Editar", "s2.editAria": "Editar teclas de {a}", "s2.press": "Pulsa el atajo…", "s2.saveKey": "Guardar", "s2.defaultKey": "Predeterminada ({k})", "s2.cancel": "Cancelar",
    "s2.customKey": "personalizada", "s2.keyBad": "Esa tecla no está permitida", "s2.gotoKeys": "número + Enter",
    "s2.reserved": "El navegador se reserva algunos atajos (como Ctrl+W); configúralos en host.json.", "s2.actions": "Botones que verán los relatores", "s2.advanced": "Avanzado", "s2.backend": "Método de entrada",
    "s2.class": "Clase de ventana / app_id", "s2.title2": "El título contiene", "s2.create": "Guardar y crear sesión ▶",
    "s2.saveContinue": "Guardar y continuar →", "s2.creating": "Abriendo el túnel…",
    "s2.ready": "✔ Listo: {t}", "s2.notReady": "✖ No listo: {r}", "s2.confirmNotReady": "La ventana de la presentación no está lista ({r}). ¿Crear la sesión de todos modos?",
    "s2.perm": "Permiso del sistema necesario: {p}", "s2.noWindows": "No se encontraron ventanas",
    "s3.title": "Comparte la sesión", "s3.h1": "Pega el enlace en el chat de la videollamada.",
    "s3.h2": "Di la contraseña por separado (de palabra o por mensaje privado).", "s3.h3": "Los relatores abren el enlace y escriben su nombre y la contraseña.",
    "s3.link": "Enlace", "s3.password": "Contraseña", "s3.manage": "Gestionar la sesión", "s3.rotate": "Cambiar contraseña (expulsa a todos)",
    "s3.end": "Terminar sesión", "s3.back": "← Destino", "s3.next": "Siguiente: relatores →", "s3.expires": "Caduca: {d}",
 "s3.conn": "{n} · {s} · WebRTC P2P {w}", "s3.https": "HTTPS", "s3.plain": "SIN CIFRAR",
    "s3.on": "activado", "s3.off": "desactivado", "s3.noIp": " · el túnel no informa de la IP de los relatores (recomendado: aprobación manual)",
    "s3.confirmEnd": "¿Terminar la sesión? Todos los relatores perderán el acceso.", "s3.confirmRotate": "Se generará una contraseña nueva y se expulsará a todos los relatores. ¿Continuar?",
    "s4.title": "Relatores", "s4.lead": "El nombre es una etiqueta elegida por cada persona, no una identidad verificada.",
    "s4.name": "Nombre", "s4.state": "Estado", "s4.conn": "Conexión", "s4.extra": "Permisos extra", "s4.activity": "Actividad",
    "s4.back": "← Sesión", "s4.none": "Nadie todavía. Comparte el enlace y la contraseña (paso 3).", "s4.noActivity": "Aún no hay órdenes.",
    "s4.online": "en línea", "s4.offline": "desconectado", "s4.approve": "Aprobar", "s4.disable": "Deshabilitar", "s4.enable": "Habilitar", "s4.revoke": "Expulsar",
    "s4.allow": "Permitir {a} a {n}",
    "state.pending": "esperando aprobación", "state.enabled": "habilitado", "state.disabled": "deshabilitado", "state.revoked": "expulsado",
    "hist.executed": "ejecutada", "hist.rejected": "rechazada", "hist.failed": "falló", "hist.expired": "caducada", "hist.dropped": "descartada",
    "chip.none": "Sin sesión", "chip.online": "Conectado", "chip.reconnecting": "Reconectando…", "chip.paused": "En pausa", "chip.ready": "Destino listo", "chip.notReady": "Destino no listo",
    pause: "Pausar control", resume: "Reanudar control", quit: "Salir de Relevo", back: "← Atrás", copy: "Copiar", copied: "Copiado", show: "Mostrar", hide: "Ocultar",
    "quit.confirm": "¿Salir? Se terminará la sesión activa.", "quit.done": "Relevo se ha cerrado.",
    "err.chooseMode": "Elige cómo se conectarán los relatores", "err.insecure": "Confirma que aceptas un túnel sin cifrar, o elige otro",
    "err.panel": "Panel desconectado",
    "reason.no_target": "elige la ventana de la presentación", "reason.target_not_found": "la ventana de la presentación no está abierta",
    "reason.unverified_optin": "este método no puede comprobar la ventana con foco; activa «enviar sin verificar» o elige otro",
    "reason.accessibility_permission": "concede el permiso de Accesibilidad a relevo-host (Ajustes del Sistema → Privacidad y seguridad → Accesibilidad)",
    "reason.no_focus_info": "el gestor de ventanas no informa de la ventana con foco",
    "warn.tunnel_down": "El túnel se cayó; reconectando…", "warn.tunnel_failed": "No se pudo reabrir el túnel; reintentando…", "warn.link_changed": "El enlace cambió: comparte el nuevo",
    "profile.presentation": "Otra (flechas, F5 para comenzar)", "profile.presentation-clicker": "Otra (Re Pág / Av Pág, como un clicker)", "profile.presentation-full": "Otra (todas las acciones)",
    "profile.google-slides": "Presentaciones de Google", "profile.powerpoint": "PowerPoint (escritorio)", "profile.powerpoint-web": "PowerPoint para la web", "profile.keynote": "Keynote (macOS)", "profile.libreoffice": "LibreOffice Impress",
    "action.presentation.next": "Siguiente", "action.presentation.previous": "Anterior", "action.presentation.first": "Primera diapositiva", "action.presentation.last": "Última diapositiva",
    "action.presentation.blank": "Pantalla negra", "action.presentation.goto": "Ir a diapositiva", "action.presentation.start": "Iniciar presentación", "action.presentation.stop": "Salir de la presentación",
  },
};
let lang = (() => { try { return localStorage.getItem("relevo.lang") || "en"; } catch { return "en"; } })();
const t = (k, v = {}) => (I18N[lang][k] ?? I18N.en[k] ?? k).replace(/\{(\w+)\}/g, (_, x) => v[x] ?? "");
const tOr = (k, fallback) => (I18N[lang][k] ?? I18N.en[k] ?? fallback);
function applyLang() {
  document.documentElement.lang = lang;
  for (const e of document.querySelectorAll("[data-i18n]")) e.textContent = t(e.dataset.i18n);
  for (const b of document.querySelectorAll("[data-lang]")) b.setAttribute("aria-pressed", String(b.dataset.lang === lang));
  document.title = "Relevo · " + t("host");
}
function setLang(l) {
  lang = l;
  try { localStorage.setItem("relevo.lang", l); } catch {}
  applyLang();
  modesBuilt = false; settingsBuilt = false; if (st) render();
  // Persist for the tray and terminal too.
  if (st && st.config.language !== l) api("PUT", "/api/settings", { ...st.config, language: l }).catch(() => {});
}
const reasonText = (s) => (s.reasonCode && I18N.en["reason." + s.reasonCode] ? t("reason." + s.reasonCode) : s.reason);
const actionTitle = (a) => tOr("action." + a.id, a.title);

// ---------- API ----------
async function api(method, path, body) {
  const res = await fetch(path, { method, headers: { Authorization: "Bearer " + token, ...(body ? { "Content-Type": "application/json" } : {}) }, body: body ? JSON.stringify(body) : undefined });
  const data = await res.json().catch(() => ({}));
  if (!res.ok) throw new Error(data.error || "HTTP " + res.status);
  return data;
}
function flash(msg) { const f = $("flash"); f.textContent = msg; f.hidden = !msg; if (msg) setTimeout(() => (f.hidden = true), 7000); }
async function act(fn) { try { await fn(); await refresh(); } catch (e) { flash(e.message); } }

// ---------- steps ----------
let st = null, step = 0, revealed = null, modesBuilt = false, settingsBuilt = false, editing = false, hadSession = null, windowsLoaded = false;
function go(n) {
  if (!st) return;
  if ((n === 3 || n === 4) && !st.session) n = 2;
  step = n;
  for (const p of document.querySelectorAll("[data-panel]")) p.hidden = Number(p.dataset.panel) !== n;
  if (n === 2 && !windowsLoaded) loadWindows();
  renderStepper();
  window.scrollTo({ top: 0 });
}
function renderStepper() {
  const done = { 1: !!st.session, 2: st.ready, 3: !!st.session && (st.participants || []).length > 0, 4: false };
  for (const b of document.querySelectorAll("[data-step]")) {
    const n = Number(b.dataset.step);
    b.disabled = (n === 3 || n === 4) && !st.session;
    b.classList.toggle("done", !!done[n] && n !== step);
    if (n === step) b.setAttribute("aria-current", "step"); else b.removeAttribute("aria-current");
  }
  const pending = (st.participants || []).filter((p) => p.state === "pending").length;
  $("pending-badge").hidden = !pending; $("pending-badge").textContent = String(pending);
}

// ---------- render ----------
function render() {
  const s = st;
  if (hadSession !== null && hadSession !== !!s.session) go(s.session ? 3 : 1); // created → share; ended → start again
  hadSession = !!s.session;
  // header chips
  $("chip-conn").textContent = s.session ? (s.paused ? t("chip.paused") : s.connected ? t("chip.online") : t("chip.reconnecting")) : t("chip.none");
  $("chip-conn").className = "chip " + (s.session ? (s.connected && !s.paused ? "ok" : "warn") : "muted");
  $("chip-ready").textContent = s.ready ? t("chip.ready") : t("chip.notReady");
  $("chip-ready").className = "chip " + (s.ready ? "ok" : "warn");
  $("pause").hidden = !s.session; $("pause").textContent = s.paused ? t("resume") : t("pause");
  // step 1
  $("s1-locked").hidden = !s.session; $("s1-form").disabled = !!s.session;
  if (!s.session && !modesBuilt) buildModes(s);
  $("ended").textContent = !s.session && s.endedReason ? t("s1.ended", { r: s.endedReason }) : "";
  // step 2
  $("ready").textContent = s.ready ? t("s2.ready", { t: s.target.label }) : t("s2.notReady", { r: reasonText(s) });
  $("ready").className = "status " + (s.ready ? "ok" : "bad");
  $("perms").replaceChildren(...(s.permissions || []).map((p) => el("li", t("s2.perm", { p }))));
  if (!editing && !settingsBuilt) buildSettings(s);
  $("create").hidden = !!s.session; $("save-continue").hidden = !s.session;
  // step 3
  if (s.session) {
    $("link").textContent = s.session.joinUrl;
    $("pw").textContent = revealed ?? "••••••••••••";
    $("reveal").textContent = revealed ? t("hide") : t("show");
    $("expires").textContent = t("s3.expires", { d: new Date(s.session.expiresAt).toLocaleString(lang) }) + (s.connError ? " · " + s.connError : "");
    const c = s.connection || {};
    $("conn-info").textContent = t("s3.conn", { n: c.provider === "url" ? t("prov.url") : c.name || c.provider, s: c.secure ? t("s3.https") : t("s3.plain"), w: c.webrtc ? t("s3.on") : t("s3.off") }) + (c.clientIp ? "" : t("s3.noIp"));
    $("conn-warn").textContent = c.warningCode ? t("warn." + c.warningCode) : c.warning || "";
  } else { revealed = null; }
  renderPeople(s);
  renderStepper();
}

function buildModes(s) {
  modesBuilt = true;
  const cur = s.config.connection || { mode: "ask" };
  const opts = [];
  for (const tn of s.tunnels) opts.push({ id: "tunnel:" + tn.id, title: tn.name, builtin: tn.builtin, note: tOr("tunnel." + tn.id, tn.notes), enabled: tn.installed, insecure: !tn.secure, hint: tn.installed ? "" : tn.installHint });
  opts.push({ id: "url", title: t("mode.url"), note: t("mode.url.note"), enabled: true });
  const prev = document.querySelector("input[name=mode]:checked")?.value;
  const sel = prev || (cur.mode === "tunnel" ? "tunnel:" + cur.tunnel : cur.mode === "url" ? "url" : (opts.find((o) => o.enabled) || {}).id);
  const firstEnabled = (opts.find((o) => o.enabled) || {}).id;
  $("modes").replaceChildren(...opts.map((o) => {
    const r = el("input"); r.type = "radio"; r.name = "mode"; r.value = o.id; r.disabled = !o.enabled; r.checked = o.id === sel;
    r.onchange = modeChanged;
    if (o.insecure) r.dataset.insecure = "1";
    const l = el("label"); if (!o.enabled) l.className = "off";
    const head = el("strong", o.title);
    if (o.id === firstEnabled) head.append(el("span", t("s1.recommended"), "tag ok"));
    if (o.builtin) head.append(el("span", t("s1.builtin"), "tag muted"));
    if (!o.enabled) head.append(el("span", t("s1.notInstalled"), "tag muted"));
    const body = el("span"); body.append(head, el("small", o.note + (o.hint ? " — " + o.hint : "")));
    l.append(r, body); return l;
  }));
  if (!prev) { $("own-url").value = cur.publicUrl || ""; $("own-port").value = cur.localPort || ""; $("approval").checked = !!s.config.requireApproval; }
  modeChanged();
}
function chosenMode() { const r = document.querySelector("input[name=mode]:checked"); return r ? r.value : ""; }
function modeChanged() {
  const m = chosenMode();
  $("url-fields").hidden = m !== "url";
  const r = document.querySelector("input[name=mode]:checked");
  $("insecure-row").hidden = !(r && r.dataset.insecure);
}
function connectionChoice() {
  const m = chosenMode();
  if (m.startsWith("tunnel:")) return { mode: "tunnel", tunnel: m.slice(7), allowInsecureTunnel: !$("insecure-row").hidden && $("insecure-ok").checked };
  if (m === "url") return { mode: "url", publicUrl: $("own-url").value.trim(), localPort: parseInt($("own-port").value, 10) || 0 };
  return null;
}

function buildSettings(s) {
  settingsBuilt = true;
  const b = $("backend"); b.replaceChildren(...[{ name: "auto", description: "auto" }, ...s.backends].map((x) => { const o = el("option", `${x.name} — ${x.description}`); o.value = x.name; return o; }));
  b.value = s.config.backend || "auto";
  $("t-class").value = s.config.target.class || ""; $("t-title").value = s.config.target.titleContains || "";
  $("t-unverified").checked = !!s.config.target.allowUnverifiedActiveWindow;
  $("unverified-row").hidden = s.target.focusCheck !== "unverified" && !s.config.target.allowUnverifiedActiveWindow;
  const p = $("profile"); p.replaceChildren(...Object.entries(s.profiles).sort(([a], [b]) => a.startsWith("presentation") - b.startsWith("presentation") || a.localeCompare(b)).map(([k, v]) => { const o = el("option", tOr("profile." + k, v)); o.value = k; return o; })); p.value = s.config.profile;
  const enabled = new Set(s.catalog.map((a) => a.id));
  bindings = { ...(s.config.bindings || {}) };
  $("actions").replaceChildren(...s.allActions.map((a) => actionRow(a, enabled.has(a.id))));
  syncWindowSelect();
}

// ---------- per-action keys ----------
// Chords use the host's whitelist names ("ctrl+F5", "super+shift+Return"); the
// server validates them, so the panel only helps to type them.
let bindings = {};
const PLAT = navigator.userAgentData?.platform || navigator.platform || "";
const MAC = /mac|iphone|ipad/i.test(PLAT);
const MOD_LABEL = MAC ? { ctrl: "⌃", alt: "⌥", shift: "⇧", super: "⌘" } : { ctrl: "Ctrl", alt: "Alt", shift: "Shift", super: /win/i.test(PLAT) ? "Win" : "Super" };
const KEY_LABEL = { Left: "←", Right: "→", Up: "↑", Down: "↓", Page_Up: "PgUp", Page_Down: "PgDn", Return: MAC ? "Return" : "Enter", Escape: "Esc", BackSpace: "Backspace", space: "Space", period: "." };
const fmtChord = (c) => c.split("+").map((p, i, a) => (i < a.length - 1 ? MOD_LABEL[p] || p : KEY_LABEL[p] || (p.length === 1 ? p.toUpperCase() : p))).join(MAC ? "" : "+");
const fmtKeys = (ks) => (ks || []).map(fmtChord).join(", ");
const CODE_KEY = { ArrowLeft: "Left", ArrowRight: "Right", ArrowUp: "Up", ArrowDown: "Down", PageUp: "Page_Up", PageDown: "Page_Down", Home: "Home", End: "End",
  Space: "space", Enter: "Return", NumpadEnter: "Return", Escape: "Escape", Backspace: "BackSpace", Period: "period", NumpadDecimal: "period" };
function keyFromEvent(e) {
  const c = e.code;
  if (/^Key[A-Z]$/.test(c)) return /^[a-z]$/i.test(e.key) ? e.key.toLowerCase() : c.slice(3).toLowerCase(); // the layout's letter when it has one
  if (/^(Digit|Numpad)[0-9]$/.test(c)) return c.slice(-1);
  if (/^F([1-9]|1[0-2])$/.test(c)) return c;
  return CODE_KEY[c];
}
const modsOf = (e) => [e.ctrlKey && "ctrl", e.altKey && "alt", e.shiftKey && "shift", e.metaKey && "super"].filter(Boolean);

function actionRow(a, checked) {
  const row = el("div"); row.className = "act";
  const l = el("label"); const c = el("input"); c.type = "checkbox"; c.value = a.id; c.checked = checked; l.append(c, el("span", actionTitle(a)));
  row.append(l);
  if (!a.keys) { row.append(el("span", t("s2.gotoKeys"), "muted small")); return row; }
  const keys = el("kbd", fmtKeys(a.keys));
  const edit = el("button", t("s2.edit")); edit.type = "button"; edit.setAttribute("aria-label", t("s2.editAria", { a: actionTitle(a) }));
  row.append(keys);
  if (a.custom) row.append(el("span", t("s2.customKey"), "tag muted"));
  row.append(edit);
  edit.onclick = () => {
    editing = true;
    let chord = "";
    const cap = el("input"); cap.className = "capture"; cap.readOnly = true; cap.placeholder = t("s2.press"); cap.setAttribute("aria-label", t("s2.editAria", { a: actionTitle(a) }));
    const save = el("button", t("s2.saveKey"), "primary"); save.type = "button"; save.disabled = true;
    const def = el("button", t("s2.defaultKey", { k: fmtKeys(a.defaultKeys) })); def.type = "button";
    const cancel = el("button", t("s2.cancel")); cancel.type = "button";
    const msg = el("span", t("s2.reserved"), "muted small");
    cap.onkeydown = (e) => {
      if (e.key === "Tab" && !e.ctrlKey && !e.altKey && !e.metaKey) return; // keep keyboard navigation
      e.preventDefault(); e.stopPropagation();
      if (e.repeat) return;
      if (["Control", "Alt", "Shift", "Meta", "AltGraph"].includes(e.key)) { cap.value = modsOf(e).map((m) => MOD_LABEL[m]).join(MAC ? "" : "+") + (MAC ? "…" : "+…"); return; }
      const k = keyFromEvent(e);
      if (!k) { chord = ""; save.disabled = true; cap.value = ""; msg.textContent = t("s2.keyBad"); msg.className = "bad small"; return; }
      chord = [...modsOf(e), k].join("+"); cap.value = fmtChord(chord); save.disabled = false;
      msg.textContent = t("s2.reserved"); msg.className = "muted small";
    };
    const done = (fn) => act(async () => { fn(); await saveSettings(); });
    save.onclick = () => done(() => { if (a.defaultKeys?.length === 1 && a.defaultKeys[0] === chord) delete bindings[a.id]; else bindings[a.id] = [chord]; });
    def.onclick = () => done(() => delete bindings[a.id]);
    cancel.onclick = () => { editing = false; settingsBuilt = false; render(); };
    row.replaceChildren(l, cap, save, def, cancel, msg);
    cap.focus();
  };
  return row;
}

let windows = [];
async function loadWindows() {
  windowsLoaded = true;
  try {
    const r = await api("GET", "/api/windows");
    windows = r.windows || [];
    const sel = $("windows");
    sel.replaceChildren(el("option", windows.length ? t("s2.pick") : t("s2.noWindows")), ...windows.map((w, i) => { const o = el("option", `${w.title || w.class}  (${w.class})`); o.value = String(i); return o; }));
    syncWindowSelect();
  } catch (e) { flash(e.message); }
}
function syncWindowSelect() {
  const cls = $("t-class").value;
  const i = windows.findIndex((w) => w.class === cls);
  $("windows").value = i >= 0 ? String(i) : "";
}

async function saveSettings() {
  const actions = [...$("actions").querySelectorAll("input:checked")].map((c) => c.value);
  await api("PUT", "/api/settings", { ...st.config, language: lang, requireApproval: $("approval").checked, backend: $("backend").value, profile: $("profile").value, actions, bindings,
    target: { class: $("t-class").value.trim(), titleContains: $("t-title").value.trim(), allowUnverifiedActiveWindow: $("t-unverified").checked } });
  editing = false; settingsBuilt = false;
}

function renderPeople(s) {
  const tb = $("people"); tb.replaceChildren();
  const ps = (s.participants || []).filter((p) => p.state !== "revoked");
  if (!ps.length) { const tr = el("tr"); const td = el("td", t("s4.none"), "muted"); td.colSpan = 5; tr.append(td); tb.append(tr); }
  const extraCaps = [...new Set(s.catalog.map((a) => a.capability))].filter((c) => c !== "presentation.navigate");
  for (const p of ps) {
    const tr = el("tr");
    tr.append(el("td", p.displayName), el("td", t("state." + p.state), p.state === "enabled" ? "ok" : "warn"), el("td", p.connected ? t("s4.online") : t("s4.offline")));
    const tdc = el("td");
    for (const cap of extraCaps) {
      const box = el("input"); box.type = "checkbox"; box.checked = p.capabilities.includes(cap);
      const titles = s.catalog.filter((a) => a.capability === cap).map(actionTitle).join(", ");
      box.setAttribute("aria-label", t("s4.allow", { a: titles, n: p.displayName }));
      box.onchange = () => act(() => api("POST", "/api/participants/" + p.participantId, { capabilities: box.checked ? [...new Set([...p.capabilities, cap])] : p.capabilities.filter((c) => c !== cap) }));
      const l = el("label"); l.className = "row small"; l.append(box, el("span", titles)); tdc.append(l);
    }
    if (!extraCaps.length) tdc.append(el("span", "—", "muted"));
    tr.append(tdc);
    const td = el("td");
    const btn = (label, state, cls) => { const b = el("button", label, cls); b.onclick = () => act(() => api("POST", "/api/participants/" + p.participantId, { state })); td.append(b); };
    if (p.state === "pending") btn(t("s4.approve"), "enabled", "primary");
    else if (p.state === "enabled") btn(t("s4.disable"), "disabled");
    else btn(t("s4.enable"), "enabled");
    btn(t("s4.revoke"), "revoked", "danger");
    tr.append(td); tb.append(tr);
  }
  const hist = (s.history || []).slice().reverse().slice(0, 40);
  $("history").replaceChildren(...(hist.length ? hist.map((e) =>
    el("li", `${new Date(e.at).toLocaleTimeString(lang)} #${e.sequence} ${tOr("action." + e.action, e.action)} · ${e.by} → ${tOr("hist." + e.status, e.status)}${e.detail ? " (" + e.detail + ")" : ""}`))
    : [el("li", t("s4.noActivity"), "muted")]));
}

async function refresh() {
  try {
    st = await api("GET", "/api/status");
    if (step === 0) { if (st.config.language && !localStorage.getItem("relevo.lang")) { lang = st.config.language; applyLang(); } go(st.session ? 3 : 1); }
    render();
  } catch { $("chip-conn").textContent = t("err.panel"); }
}

// ---------- events ----------
for (const b of document.querySelectorAll("[data-lang]")) b.onclick = () => setLang(b.dataset.lang);
for (const b of document.querySelectorAll("[data-step]")) b.onclick = () => go(Number(b.dataset.step));
for (const b of document.querySelectorAll("[data-go]")) b.onclick = () => go(Number(b.dataset.go));
for (const id of ["backend", "t-class", "t-title", "t-unverified", "profile"]) $(id).addEventListener("input", () => (editing = true));
$("actions").addEventListener("change", () => (editing = true));
$("windows").onchange = (e) => {
  const w = windows[Number(e.target.value)];
  if (!w) return;
  $("t-class").value = w.class; $("t-title").value = ""; editing = true;
  act(saveSettings); // picking the window is the main action of this step: save right away
};
$("refresh-windows").onclick = () => loadWindows();
$("profile").onchange = () => { editing = true; act(saveSettings); };
$("create").onclick = () => act(async () => {
  const connection = connectionChoice();
  if (!connection) { go(1); throw new Error(t("err.chooseMode")); }
  if (!$("insecure-row").hidden && !$("insecure-ok").checked) { go(1); throw new Error(t("err.insecure")); }
  await saveSettings();
  const fresh = await api("GET", "/api/status");
  if (!fresh.ready && !confirm(t("s2.confirmNotReady", { r: reasonText(fresh) }))) return;
  $("create").disabled = true; $("creating").hidden = false;
  try {
    await api("POST", "/api/session", { requireApproval: $("approval").checked, password: $("custom-pw").value, connection, remember: $("remember").checked });
    $("custom-pw").value = "";
  } finally { $("create").disabled = false; $("creating").hidden = true; }
});
$("save-continue").onclick = () => act(async () => { await saveSettings(); go(3); });
$("end").onclick = () => confirm(t("s3.confirmEnd")) && act(() => api("DELETE", "/api/session"));
$("pause").onclick = () => act(() => api("POST", "/api/session/pause", { paused: !st.paused }));
$("rotate").onclick = () => confirm(t("s3.confirmRotate")) && act(async () => { await api("POST", "/api/session/rotate", {}); revealed = null; });
$("reveal").onclick = () => act(async () => { revealed = revealed ? null : (await api("POST", "/api/session/reveal")).password; });
const copied = (b) => { const old = b.textContent; b.textContent = t("copied"); setTimeout(() => (b.textContent = old), 1500); };
$("copy-pw").onclick = () => act(async () => { await navigator.clipboard.writeText((await api("POST", "/api/session/reveal")).password); copied($("copy-pw")); });
$("copy-link").onclick = async () => { await navigator.clipboard.writeText($("link").textContent); copied($("copy-link")); };
$("quit").onclick = () => confirm(t("quit.confirm")) && api("POST", "/api/quit").then(() => (document.body.textContent = t("quit.done")));
applyLang();
refresh(); setInterval(refresh, 1000);

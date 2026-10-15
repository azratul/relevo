// End-to-end test: real relay + real host agent (simulated key backend) +
// real Chrome (headless, via playwright-core). Run with `make e2e`.
// CHROME=/path/to/chrome overrides the browser.
import { spawn } from "node:child_process";
import { mkdtempSync, writeFileSync, existsSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import assert from "node:assert/strict";
import { chromium, firefox } from "playwright-core";

const root = new URL("../../../", import.meta.url).pathname;
// BROWSER=chrome (default) | firefox (real Firefox through WebDriver BiDi)
const which = process.env.BROWSER ?? "chrome";
const exe = which === "firefox"
  ? process.env.FIREFOX ?? ["/usr/bin/firefox", "/usr/lib/firefox/firefox"].find(existsSync)
  : process.env.CHROME ?? ["/usr/bin/google-chrome-stable", "/usr/bin/google-chrome", "/usr/bin/chromium", "/usr/bin/chromium-browser"].find(existsSync);
if (!exe) { console.error(`No ${which} found; set CHROME or FIREFOX`); process.exit(2); }
console.log(`browser: ${which} (${exe})`);
const port = 18000 + Math.floor(Math.random() * 1000);
const base = `http://127.0.0.1:${port}`;
const procs = [];
const results = [];
let a, b;

function start(cmd, args, env = {}) {
  const p = spawn(cmd, args, { env: { ...process.env, ...env }, stdio: ["ignore", "pipe", "pipe"] });
  procs.push(p);
  let out = "";
  p.stdout.on("data", (d) => (out += d));
  p.stderr.on("data", (d) => (out += d));
  return { p, out: () => out };
}
async function waitFor(fn, ms = 10000, what = "condition") {
  const t0 = Date.now();
  for (;;) {
    const v = await fn().catch(() => undefined);
    if (v) return v;
    if (Date.now() - t0 > ms) throw new Error("timeout waiting for " + what);
    await new Promise((r) => setTimeout(r, 100));
  }
}
async function step(name, fn) {
  try { await fn(); results.push(["PASS", name]); console.log("PASS", name); }
  catch (e) {
    results.push(["FAIL", name]); console.log("FAIL", name, "\n   ", e.message.split("\n")[0]);
    for (const [n, pg] of [["A", a], ["B", b]]) if (pg) console.log(`    [${n}] ${(await pg.locator("body").innerText().catch(() => "")).replace(/\s+/g, " ").slice(0, 300)}`);
  }
}

// The host runs the relay itself (ADR-0001); locally we use "own URL" mode on
// loopback instead of a real tunnel.
const relay = { out: () => host.out() };
const cfgDir = mkdtempSync(join(tmpdir(), "relevo-e2e-"));
const cfgPath = join(cfgDir, "host.json");
writeFileSync(cfgPath, JSON.stringify({
  backend: "sim", target: { class: "relevo-sim" }, profile: "presentation-full", adminListen: "127.0.0.1:0",
  connection: { mode: "url", publicUrl: base, localPort: port, allowInsecureTunnel: true, stunServers: [] },
}));
if (process.env.NO_P2P) process.env.RELEVO_TEST_RTC_NO_ROUTE = "1";
let host;
host = start(join(root, "bin/relevo-host"), ["--config", cfgPath, "--no-tray", "--no-browser", "--start"]);
const hostOut = await waitFor(async () => /(?:Password|Contraseña):\s+(\S+)/.test(host.out()) && host.out(), 10000, "host session");
const link = hostOut.match(/(?:Link|Enlace):\s+(\S+)/)[1];
const password = hostOut.match(/(?:Password|Contraseña):\s+(\S+)/)[1];
const adminURL = hostOut.match(/(?:Local panel|Panel local): (\S+)/)[1];
const adminBase = adminURL.split("/#")[0];
const adminToken = adminURL.split("#t=")[1];
const admin = async (method, path, body) => {
  const r = await fetch(adminBase + path, { method, headers: { Authorization: "Bearer " + adminToken, "Content-Type": "application/json" }, body: body && JSON.stringify(body) });
  return r.json();
};
const executed = async () => (await admin("GET", "/api/status")).history.filter((h) => h.status === "executed");
await waitFor(async () => (await admin("GET", "/api/status")).connected, 10000, "host connected");

const browser = which === "firefox"
  ? await firefox.launch({ channel: "moz-firefox", executablePath: exe, headless: true })
  : await chromium.launch({ executablePath: exe, headless: true });
const ctxA = await browser.newContext();
// isMobile is Chromium-only; Firefox gets a phone-sized touch context.
const ctxB = await browser.newContext(which === "firefox" ? { viewport: { width: 390, height: 844 }, hasTouch: true } : { viewport: { width: 390, height: 844 }, hasTouch: true, isMobile: true });
// Keep references to DataChannels so a test can drop the P2P path.
await ctxA.addInitScript(() => {
  const orig = RTCPeerConnection.prototype.createDataChannel;
  window.__dcs = [];
  RTCPeerConnection.prototype.createDataChannel = function (...args) { const dc = orig.apply(this, args); window.__dcs.push(dc); return dc; };
});
a = await ctxA.newPage();
b = await ctxB.newPage();
const consoleErrors = [];
for (const pg of [a, b]) pg.on("console", (m) => m.type() === "error" && consoleErrors.push(m.text()));

await step("link alone does not grant access (wrong password shows error)", async () => {
  await a.goto(link);
  await a.fill("#name", "Ana");
  await a.fill("#password", "wrong-password");
  await a.click("button[type=submit]");
  await a.getByText("Wrong password").waitFor({ timeout: 5000 });
  assert.equal(await a.locator("#btn-next").count(), 0);
});

await step("two speakers log in (desktop + mobile)", async () => {
  await a.fill("#password", password);
  await a.click("button[type=submit]");
  await a.locator("#btn-next:not([disabled])").waitFor({ timeout: 5000 });
  await b.goto(link);
  await b.fill("#name", "Beto");
  await b.fill("#password", password);
  await (which === "firefox" ? b.click("button[type=submit]") : b.tap("button[type=submit]"));
  await b.locator("#btn-next:not([disabled])").waitFor({ timeout: 5000 });
});

await step(process.env.NO_P2P ? "without UDP the remote stays on the tunnel (no P2P)" : "the remote upgrades to a direct WebRTC DataChannel (P2P)", async () => {
  if (process.env.NO_P2P) {
    await a.waitForTimeout(12000); // upgrade attempt: gathering 2.5 s + open 8 s
    for (const pg of [a, b]) assert.equal(await pg.locator("[data-transport]").first().getAttribute("data-transport"), "websocket");
    return;
  }
  for (const pg of [a, b]) await pg.locator("[data-transport=webrtc]").waitFor({ timeout: 10000 });
  assert.match(await a.locator("[data-transport]").first().textContent(), /P2P/);
});

if (!process.env.NO_P2P) await step("losing the P2P channel falls back to the tunnel without replaying commands", async () => {
  const before = (await executed()).length;
  await a.evaluate(() => window.__dcs.forEach((dc) => dc.close()));
  await a.locator("[data-transport=websocket]").waitFor({ timeout: 15000 });
  await a.locator("#btn-next:not([disabled])").waitFor({ timeout: 10000 });
  await new Promise((r) => setTimeout(r, 500));
  assert.equal((await executed()).length, before, "something was executed during the fallback");
});

await step("click and tap execute and show the result", async () => {
  await a.click("#btn-next");
  await a.locator(".log li.st-executed").first().waitFor({ timeout: 5000 });
  await (which === "firefox" ? b.click("#btn-prev") : b.tap("#btn-prev"));
  await b.locator(".log li.st-executed").first().waitFor({ timeout: 5000 });
  assert.match(await a.locator(".log li").first().textContent(), /Simulated/);
  const ex = await executed();
  assert.deepEqual(ex.map((e) => [e.action, e.by]), [["presentation.next", "Ana"], ["presentation.previous", "Beto"]]);
});

await step("keyboard shortcut sends once; auto-repeat ignored; typing in an input sends nothing", async () => {
  const before = (await executed()).length;
  await a.bringToFront();
  await a.locator("body").click({ position: { x: 5, y: 5 } });
  await a.keyboard.press("ArrowRight");
  await waitFor(async () => (await executed()).length === before + 1, 5000, "shortcut executed");
  await a.evaluate(() => document.dispatchEvent(new KeyboardEvent("keydown", { key: "ArrowRight", repeat: true, bubbles: true })));
  await a.locator("form.param input").first().focus();
  await a.keyboard.press("ArrowRight");
  await a.keyboard.press("ArrowLeft");
  await new Promise((r) => setTimeout(r, 600));
  assert.equal((await executed()).length, before + 1);
});

await step("Enter on a focused button is one click, not click + shortcut", async () => {
  const before = (await executed()).length;
  await a.bringToFront(); // Firefox headless delivers keys only to the front window
  await a.focus("#btn-next");
  await a.keyboard.press("Enter");
  await waitFor(async () => (await executed()).length === before + 1, 5000, "one execution");
  await a.keyboard.press("Space");
  await waitFor(async () => (await executed()).length === before + 2, 5000, "second execution");
  await new Promise((r) => setTimeout(r, 500));
  assert.equal((await executed()).length, before + 2);
});

await step("parameterized action needs capability; host grants it", async () => {
  const st = await admin("GET", "/api/status");
  const ana = st.participants.find((p) => p.displayName === "Ana");
  assert.equal(await a.locator("form.param button").isDisabled(), true, "button must be disabled without capability");
  await admin("POST", "/api/participants/" + ana.participantId, { capabilities: ["presentation.navigate", "presentation.jump"] });
  await a.locator("form.param button:not([disabled])").waitFor({ timeout: 5000 });
  await a.fill("form.param input", "7");
  await a.locator("form.param button").click();
  await waitFor(async () => (await executed()).some((e) => e.action === "presentation.goto"), 5000, "goto executed");
});

await step("state updates do not clear a half-typed input or steal focus", async () => {
  await a.fill("form.param input", "4");
  await a.focus("form.param input");
  await admin("POST", "/api/session/pause", { paused: true });
  await a.getByText("The host paused control").first().waitFor({ timeout: 5000 });
  await admin("POST", "/api/session/pause", { paused: false });
  await a.locator("#btn-next:not([disabled])").waitFor({ timeout: 5000 });
  assert.equal(await a.inputValue("form.param input"), "4");
  assert.equal(await a.evaluate(() => document.activeElement?.tagName), "INPUT");
  await a.fill("form.param input", "");
});

await step("English by default; switching to Spanish translates everything, also past commands", async () => {
  assert.equal(await a.locator("#btn-next").textContent(), "Next ▶");
  await a.locator(".lang-btn", { hasText: "ES" }).click();
  await a.locator("#btn-next", { hasText: "Siguiente ▶" }).waitFor({ timeout: 3000 });
  assert.match(await a.locator(".log li").first().textContent(), /Ejecutada|Simulado/);
  assert.match(await a.locator("[data-transport]").first().textContent(), /Conectado/);
  await a.reload(); // remembered per browser (login needed again: the token lives in memory only)
  await a.locator("button[type=submit]", { hasText: "Entrar" }).waitFor({ timeout: 5000 });
  await a.locator(".lang-btn", { hasText: "EN" }).click();
  await a.fill("#name", "Ana");
  await a.fill("#password", password);
  await a.click("button[type=submit]");
  await a.locator("#btn-next:not([disabled])", { hasText: "Next ▶" }).waitFor({ timeout: 10000 });
});

await step("pause disables the remote and shows why", async () => {
  await admin("POST", "/api/session/pause", { paused: true });
  await a.getByText("The host paused control").first().waitFor({ timeout: 5000 });
  assert.equal(await a.locator("#btn-next").isDisabled(), true);
  await admin("POST", "/api/session/pause", { paused: false });
  await a.locator("#btn-next:not([disabled])").waitFor({ timeout: 5000 });
});

await step("revoking one speaker ends only their access", async () => {
  const st = await admin("GET", "/api/status");
  const beto = st.participants.find((p) => p.displayName === "Beto");
  await admin("POST", "/api/participants/" + beto.participantId, { state: "revoked" });
  await b.getByText("The host removed your access").waitFor({ timeout: 5000 });
  await a.click("#btn-next");
  await a.locator(".log li").first().filter({ hasText: "Done" }).waitFor({ timeout: 5000 });
});

await step("no secrets in URLs or relay logs", async () => {
  assert.ok(!a.url().includes(password));
  const logs = relay.out();
  assert.ok(!logs.split(/Password:|Contraseña:/)[1]?.slice(30).includes(password), "password in relay log");
  assert.ok(!/rlv[ph]_[A-Za-z0-9_-]{20,}/.test(logs), "token in relay log");
  assert.ok(!host.out().split(/Password:|Contraseña:/)[1]?.slice(30).includes(password), "password repeated in host log");
});

await step("CSP header present and no console errors", async () => {
  const res = await fetch(link);
  assert.match(res.headers.get("content-security-policy") ?? "", /script-src 'self'/);
  assert.deepEqual(consoleErrors.filter((e) => !/401|Unauthorized/.test(e)), []);
});

await step("ending the session closes the remote", async () => {
  await admin("DELETE", "/api/session");
  await a.getByText("The host ended the session").waitFor({ timeout: 5000 });
});

await browser.close();
for (const p of procs) p.kill("SIGTERM");
const failed = results.filter((r) => r[0] === "FAIL").length;
console.log(`\n${results.length - failed}/${results.length} passed`);
process.exit(failed ? 1 : 0);

// LIVE test through a real public tunnel (opt-in, needs Internet):
//   TUNNEL=localhost.run node e2e/live-tunnel.mjs      (also: ngrok, serveo, cloudflared, …)
// The host panel chooses the tunnel and creates the session; a headless
// browser joins through the public URL, upgrades to WebRTC if it can, and
// sends commands to the simulated backend. Everything is torn down at the end.
import { spawn } from "node:child_process";
import { mkdtempSync, writeFileSync, existsSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { chromium, firefox } from "playwright-core";

const root = new URL("../../../", import.meta.url).pathname;
const tunnelId = process.env.TUNNEL ?? "localhost.run";
const which = process.env.BROWSER ?? "chrome";
const exe = which === "firefox" ? "/usr/bin/firefox" : ["/usr/bin/google-chrome-stable", "/usr/bin/google-chrome", "/usr/bin/chromium"].find(existsSync);
const cfgDir = mkdtempSync(join(tmpdir(), "relevo-live-"));
writeFileSync(join(cfgDir, "host.json"), JSON.stringify({ backend: "sim", profile: "presentation", adminListen: "127.0.0.1:0", connection: { mode: "ask" } }));
// HOST_PATH="" proves the binary needs no external program for built-in tunnels.
const env = { ...process.env, ...(process.env.HOST_PATH !== undefined ? { PATH: process.env.HOST_PATH } : {}) };
const host = spawn(join(root, "bin/relevo-host"), ["--config", join(cfgDir, "host.json"), "--no-tray", "--no-browser"], { stdio: ["ignore", "pipe", "pipe"], env });
let out = "";
host.stdout.on("data", (d) => (out += d)); host.stderr.on("data", (d) => (out += d));
const wait = async (fn, ms, what) => { const t0 = Date.now(); for (;;) { const v = await fn().catch(() => null); if (v) return v; if (Date.now() - t0 > ms) throw new Error("timeout: " + what); await new Promise((r) => setTimeout(r, 200)); } };
const result = { tunnel: tunnelId, browser: which };
let browser;
try {
  const adminURL = await wait(async () => out.match(/(?:Local panel|Panel local): (\S+)/)?.[1], 10000, "panel");
  const [adminBase, adminTok] = adminURL.split("/#t=");
  const admin = async (m, p, b) => (await fetch(adminBase + p, { method: m, headers: { Authorization: "Bearer " + adminTok, "Content-Type": "application/json" }, body: b && JSON.stringify(b) })).json();
  browser = which === "firefox" ? await firefox.launch({ channel: "moz-firefox", executablePath: exe, headless: true }) : await chromium.launch({ executablePath: exe, headless: true });
  const panel = await (await browser.newContext()).newPage();
  await panel.goto(adminURL);
  const radio = panel.locator(`input[name=mode][value="tunnel:${tunnelId}"]`);
  await radio.check({ timeout: 10000 });
  if (await radio.getAttribute("data-insecure")) await panel.check("#insecure-ok"); // explicit consent for bore.pub
  panel.on("dialog", (d) => d.accept());
  await panel.click('[data-panel="1"] [data-go="2"]');
  const t0 = Date.now();
  await panel.click("#create");
  await panel.locator('[data-panel="3"]:not([hidden])').waitFor({ timeout: 60000 });
  result.tunnelSetupMs = Date.now() - t0;
  const link = (await panel.textContent("#link")).trim();
  result.link = link;
  result.connInfo = (await panel.textContent("#conn-info")).trim();
  const { password } = await admin("POST", "/api/session/reveal");
  const sp = await (await browser.newContext()).newPage();
  await sp.goto(link, { timeout: 30000 });
  // Free plans of some tunnels show a warning page first: click through like a person would.
  if (!(await sp.locator("#name").isVisible({ timeout: 5000 }).catch(() => false))) {
    result.interstitial = (await sp.title()) || "yes";
    const btn = sp.getByRole("button", { name: /visit|continue|proceed|continuar|acceder/i }).or(sp.getByRole("link", { name: /visit|continue|proceed/i }));
    await btn.first().click({ timeout: 10000 });
    await sp.locator("#name").waitFor({ timeout: 20000 });
  }
  await sp.fill("#name", "Live");
  await sp.fill("#password", password);
  await sp.click("button[type=submit]");
  await sp.locator("#btn-next:not([disabled])").waitFor({ timeout: 20000 });
  result.transport = await sp.locator("[data-transport=webrtc]").waitFor({ timeout: 10000 }).then(() => "webrtc", () => "websocket (tunnel)");
  const times = [];
  for (let i = 0; i < 10; i++) {
    const n = await sp.locator(".log li.st-executed").count();
    const s = Date.now();
    await sp.click("#btn-next");
    await sp.waitForFunction((k) => document.querySelectorAll(".log li.st-executed").length > k || document.querySelectorAll(".log li").length >= 8 && document.querySelector(".log li").className === "st-executed", n, { timeout: 15000 });
    times.push(Date.now() - s);
    await sp.waitForTimeout(250);
  }
  times.sort((x, y) => x - y);
  result.clickToResultMs = { p50: times[5], max: times[9] };
  const hist = (await admin("GET", "/api/status")).history.filter((h) => h.status === "executed").length;
  result.executedOnHost = hist;
  await admin("DELETE", "/api/session");
  await sp.getByText("The host ended the session").waitFor({ timeout: 15000 });
  result.ended = true;
  console.log(JSON.stringify(result, null, 2));
} catch (e) {
  console.log(JSON.stringify(result, null, 2));
  console.error("FAILED:", e.message, "\n--- host output tail ---\n" + out.split("\n").slice(-15).join("\n"));
  process.exitCode = 1;
} finally {
  await browser?.close();
  host.kill("SIGTERM");
}

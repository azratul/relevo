// E2E of the host panel wizard (offline: "own URL" mode, simulated backend).
import { spawn } from "node:child_process";
import { mkdtempSync, writeFileSync, existsSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import assert from "node:assert/strict";
import { chromium } from "playwright-core";

const root = new URL("../../../", import.meta.url).pathname;
const exe = process.env.CHROME ?? ["/usr/bin/google-chrome-stable", "/usr/bin/google-chrome", "/usr/bin/chromium"].find(existsSync);
const port = 19000 + Math.floor(Math.random() * 900);
const dir = mkdtempSync(join(tmpdir(), "relevo-panel-"));
writeFileSync(join(dir, "host.json"), JSON.stringify({ backend: "sim", profile: "presentation", adminListen: "127.0.0.1:0", connection: { mode: "ask" } }));
const host = spawn(join(root, "bin/relevo-host"), ["--config", join(dir, "host.json"), "--no-tray", "--no-browser"], { stdio: ["ignore", "pipe", "pipe"] });
let out = ""; host.stdout.on("data", (d) => (out += d)); host.stderr.on("data", (d) => (out += d));
const wait = async (fn, ms) => { const t0 = Date.now(); for (;;) { const v = await fn().catch(() => null); if (v) return v; if (Date.now() - t0 > ms) throw new Error("timeout"); await new Promise((r) => setTimeout(r, 100)); } };
const results = [];
const browser = await chromium.launch({ executablePath: exe, headless: true });
const page = await (await browser.newContext()).newPage();
page.on("dialog", (d) => d.accept());
async function step(name, fn) {
  try { await fn(); results.push(true); console.log("PASS", name); }
  catch (e) { results.push(false); console.log("FAIL", name, "\n   ", e.message.split("\n")[0], "\n    page:", (await page.locator("body").innerText()).replace(/\s+/g, " ").slice(0, 250)); }
}
const visible = (n) => page.locator(`[data-panel="${n}"]:not([hidden])`);
// Only one step may be VISIBLE (computed style, not just the attribute).
async function onlyStep(n) {
  for (let i = 1; i <= 4; i++) assert.equal(await page.locator(`[data-panel="${i}"]`).isVisible(), i === n, `step ${i} visibility`);
}
try {
  const url = await wait(async () => out.match(/(?:Local panel|Panel local): (\S+)/)?.[1], 10000);
  await page.goto(url);

  await step("starts in English at step 1; steps 3 and 4 locked without a session", async () => {
    await visible(1).waitFor({ timeout: 5000 });
    assert.equal(await page.locator('[data-panel="1"] h2').textContent(), "How will speakers connect?");
    assert.equal(await page.locator('[data-step="1"]').getAttribute("aria-current"), "step");
    assert.ok(await page.locator('[data-step="3"]').isDisabled());
    assert.ok(await page.locator('[data-step="4"]').isDisabled());
    assert.equal(await page.locator("input[name=mode]:checked").getAttribute("value"), "tunnel:localhost.run"); // recommended default
    await onlyStep(1);
    assert.equal(await page.locator("#relay-fields").isVisible(), false);
    assert.equal(await page.locator("#insecure-row").isVisible(), false);
  });

  await step("ES/EN switch translates the panel", async () => {
    await page.click('[data-lang="es"]');
    assert.equal(await page.locator('[data-panel="1"] h2').textContent(), "¿Cómo se conectarán los relatores?");
    assert.match(await page.locator('[data-step="2"]').textContent(), /Destino y acciones/);
    await page.click('[data-lang="en"]');
    assert.match(await page.locator('[data-step="2"]').textContent(), /Target & actions/);
  });

  await step("step 1 → step 2: pick the presentation window, target becomes ready", async () => {
    await page.check('input[name=mode][value="url"]');
    await page.fill("#own-url", "https://relevo.invalid");
    await page.fill("#own-port", String(port));
    await page.locator("summary", { hasText: "More options" }).click();
    await page.check("#approval");
    await page.click('[data-panel="1"] [data-go="2"]');
    await visible(2).waitFor();
    await onlyStep(2);
    await wait(async () => (await page.locator("#windows option").count()) > 1, 5000);
    await page.selectOption("#windows", { label: "Simulated presentation  (relevo-sim)" });
    await page.locator("#ready.ok").waitFor({ timeout: 5000 });
    assert.match(await page.textContent("#ready"), /Ready/);
  });

  await step("per-action keys: application defaults, Edit captures a shortcut, Default restores it", async () => {
    const row = () => page.locator(".act", { hasText: "Start presentation" });
    await page.selectOption("#profile", "google-slides");
    await row().locator("kbd", { hasText: "Ctrl+F5" }).waitFor({ timeout: 5000 });
    await row().locator("button", { hasText: "Edit" }).click();
    await page.focus(".act .capture");
    await page.keyboard.press("Control+Shift+KeyP");
    assert.equal(await page.inputValue(".act .capture"), "Ctrl+Shift+P");
    await page.keyboard.press("F5"); // must not reload the panel
    assert.equal(await page.inputValue(".act .capture"), "F5");
    await page.keyboard.press("Alt+Shift+Home");
    await row().locator("button", { hasText: "Save" }).click();
    await row().locator("kbd", { hasText: "Alt+Shift+Home" }).waitFor({ timeout: 5000 });
    await row().locator(".tag", { hasText: "custom" }).waitFor();
    const cfg = await (await fetch(url.replace(/#.*/, "") + "api/status", { headers: { Authorization: "Bearer " + url.split("#t=")[1] } })).json();
    assert.deepEqual(cfg.config.bindings, { "presentation.start": ["alt+shift+Home"] });
    await row().locator("button", { hasText: "Edit" }).click();
    await row().locator("button", { hasText: "Default (Ctrl+F5)" }).click();
    await row().locator("kbd", { hasText: "Ctrl+F5" }).waitFor({ timeout: 5000 });
    assert.equal(await row().locator(".tag").count(), 0);
    await page.selectOption("#profile", "presentation");
    await row().locator("kbd", { hasText: "F5" }).waitFor({ timeout: 5000 });
  });

  await step("create session → jumps to step 3 with the link and a hidden password", async () => {
    await page.click("#create");
    await visible(3).waitFor({ timeout: 15000 });
    await onlyStep(3);
    assert.match(await page.textContent("#link"), /^https:\/\/relevo\.invalid\/s\//);
    assert.equal(await page.textContent("#pw"), "••••••••••••");
    await page.click("#reveal");
    await wait(async () => /^[a-z2-9]{4}-/.test(await page.textContent("#pw")), 3000);
    assert.ok(!(await page.locator('[data-step="3"]').isDisabled()));
  });

  await step("a pending speaker shows a badge on step 4 and can be approved there", async () => {
    const pw = await page.textContent("#pw");
    const sid = (await page.textContent("#link")).split("/s/")[1];
    const r = await fetch(`http://127.0.0.1:${port}/api/v1/sessions/${sid}/join`, { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ displayName: "Ana", password: pw }) });
    assert.equal(r.status, 200);
    await page.locator("#pending-badge:not([hidden])", { hasText: "1" }).waitFor({ timeout: 5000 });
    await page.click('[data-step="4"]');
    await visible(4).waitFor();
    await onlyStep(4);
    await page.locator("#people button", { hasText: "Approve" }).click();
    await page.locator("#people td", { hasText: "enabled" }).waitFor({ timeout: 5000 });
    await page.locator("#pending-badge[hidden]").waitFor({ state: "attached", timeout: 5000 });
  });

  await step("ending the session returns to step 1", async () => {
    await page.click('[data-step="3"]');
    await page.locator("summary", { hasText: "Manage session" }).click();
    await page.click("#end");
    await visible(1).waitFor({ timeout: 10000 });
    assert.ok(await page.locator('[data-step="3"]').isDisabled());
  });
} finally {
  await browser.close();
  host.kill("SIGTERM");
}
console.log(`\n${results.filter(Boolean).length}/${results.length} passed`);
process.exit(results.every(Boolean) ? 0 : 1);

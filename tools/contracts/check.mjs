// Contract checks (run in CI): OpenAPI lint, AsyncAPI validation, JSON
// Schema dialect subset, examples under draft-07 AND 2020-12, coverage.
import { readFileSync, readdirSync, statSync } from "node:fs";
import { join, relative, dirname, basename } from "node:path";
import { execFileSync } from "node:child_process";
import { fileURLToPath } from "node:url";
import Ajv from "ajv";
import Ajv2020 from "ajv/dist/2020.js";
import { Parser, fromFile } from "@asyncapi/parser";
import YAML from "yaml";

const here = dirname(fileURLToPath(import.meta.url));
const api = join(here, "../../api");
const root = join(here, "../..");
let failures = 0;
const fail = (msg) => { failures++; console.error("FAIL", msg); };
const ok = (msg) => console.log("ok  ", msg);
const walk = (d) => readdirSync(d).flatMap((f) => (statSync(join(d, f)).isDirectory() ? walk(join(d, f)) : [join(d, f)]));

// 1. OpenAPI
try {
  execFileSync(join(here, "node_modules/.bin/redocly"), ["lint", "--config", join(here, "redocly.yaml"), join(api, "openapi.yaml")], { stdio: "pipe" });
  ok("openapi.yaml lint (Redocly)");
} catch (e) {
  fail("openapi.yaml lint\n" + e.stdout?.toString() + e.stderr?.toString());
}

// 2. AsyncAPI (official parser, resolves the external schema/example refs)
{
  const parser = new Parser();
  const { document, diagnostics } = await fromFile(parser, join(api, "asyncapi.yaml")).parse();
  const errors = diagnostics.filter((d) => d.severity === 0);
  for (const d of diagnostics.filter((d) => d.severity === 1)) console.log("warn", "asyncapi:", d.message, d.path?.join("/"));
  if (errors.length || !document) fail("asyncapi.yaml\n" + errors.map((d) => ` - ${d.message} at ${d.path?.join("/")}`).join("\n"));
  else ok(`asyncapi.yaml valid (${document.allMessages().length} messages)`);
}

// 3. Dialect subset (ADR-0012)
const FORBIDDEN = ["$schema", "$dynamicRef", "$dynamicAnchor", "$recursiveRef", "$recursiveAnchor", "$anchor", "prefixItems", "unevaluatedProperties", "unevaluatedItems", "dependentRequired", "dependentSchemas", "contentSchema", "definitions"];
const schemaFiles = walk(join(api, "schemas")).filter((f) => f.endsWith(".json"));
const schemas = Object.fromEntries(schemaFiles.map((f) => [relative(join(api, "schemas"), f), JSON.parse(readFileSync(f, "utf8"))]));
const scan = (node, path, file) => {
  if (Array.isArray(node)) return node.forEach((n, i) => scan(n, `${path}/${i}`, file));
  if (node && typeof node === "object") {
    for (const [k, v] of Object.entries(node)) {
      if (FORBIDDEN.includes(k)) fail(`${file}${path}: keyword ${k} is outside the common dialect subset`);
      if (k === "items" && Array.isArray(v)) fail(`${file}${path}: array-form items`);
      if (k === "$ref" && typeof v === "string" && /^[a-z]+:/i.test(v)) fail(`${file}${path}: absolute $ref ${v}`);
      scan(v, `${path}/${k}`, file);
    }
  }
};
for (const [f, s] of Object.entries(schemas)) scan(s, "#", f);
ok(`${schemaFiles.length} schemas use only the common draft-07/2020-12 subset`);

// 4. Examples validated with both dialects; verdicts must match the folder.
const BASE = "https://relevo.invalid/api/schemas/";
function makeAjv(Cls) {
  const ajv = new Cls({ strict: true, allErrors: false, validateFormats: false, strictTypes: false, strictRequired: false, allowUnionTypes: true });
  for (const [f, s] of Object.entries(schemas)) ajv.addSchema({ ...s, $id: BASE + f });
  return ajv;
}
const validators = { "draft-07": makeAjv(Ajv), "2020-12": makeAjv(Ajv2020) };
const exampleFiles = walk(join(api, "examples")).filter((f) => f.endsWith(".json"));
let checked = 0;
for (const f of exampleFiles) {
  const rel = relative(join(api, "examples"), f);
  const [kind, dir] = rel.split("/");
  const schema = `${dir}/${basename(f, ".json").split("--")[0]}.json`;
  if (!schemas[schema]) { fail(`${rel}: no schema ${schema}`); continue; }
  const data = JSON.parse(readFileSync(f, "utf8"));
  const verdicts = Object.entries(validators).map(([name, ajv]) => [name, ajv.validate(BASE + schema, data), ajv.errorsText()]);
  const expected = kind === "valid";
  for (const [name, v, txt] of verdicts) if (v !== expected) fail(`${rel}: ${name} says ${v ? "valid" : "invalid (" + txt + ")"}, expected ${kind}`);
  checked++;
}
ok(`${checked} examples: same verdict under draft-07 and 2020-12, matching valid/invalid`);

// 5. Coverage: every ws schema is an AsyncAPI message, every http schema is used by OpenAPI,
//    every error code is documented in docs/protocol.md.
const before = failures;
const asyncText = readFileSync(join(api, "asyncapi.yaml"), "utf8");
const openText = readFileSync(join(api, "openapi.yaml"), "utf8");
for (const f of Object.keys(schemas)) {
  if (f.startsWith("ws/") && !asyncText.includes(`./schemas/${f}`)) fail(`asyncapi.yaml does not reference ${f}`);
  if (f.startsWith("http/") && !openText.includes(`./schemas/${f}`)) fail(`openapi.yaml does not reference ${f}`);
}
const protocolDoc = readFileSync(join(root, "docs/protocol.md"), "utf8");
for (const code of schemas["common.json"].$defs.ErrorCode.enum) if (!protocolDoc.includes("`" + code + "`")) fail(`docs/protocol.md does not document ${code}`);
YAML.parse(openText); // syntax
if (failures === before) ok("coverage: schemas referenced by the documents; error codes documented");

if (failures) { console.error(`\n${failures} contract check(s) failed`); process.exit(1); }
console.log("\nall contract checks passed");

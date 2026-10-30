import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { test } from "node:test";
import { allowedOrigin, relay } from "./site/oauth/callback/relay.js";

const NOW = 1_800_000_000_000;
const PAGE = "https://alexis-marcel.github.io/eriphe-system/oauth/callback/";
const ORIGINS = JSON.parse(readFileSync(new URL("./origins.json", import.meta.url), "utf8"));

// A state as eriphe-core makes it: e1.<base64url payload>.<MAC> (only the workspace checks the MAC).
function state(w, x = NOW / 1000 + 600, n = "AbCdEfGhIjKlMnOpQr12") {
  const payload = Buffer.from(JSON.stringify({ w, n, x })).toString("base64url");
  return `e1.${payload}.${Buffer.from("a-32-byte-signature-............").toString("base64url")}`;
}

function call(params) {
  const q = typeof params === "string" ? params : new URLSearchParams(params).toString();
  return relay(`${PAGE}?${q}`, NOW);
}

test("a valid state goes back to the workspace, with only code and state", () => {
  const s = state("https://192.168.1.2:8443");
  const to = call({ code: "4/0Ab-code", state: s, scope: "https://www.googleapis.com/auth/drive", authuser: "0", prompt: "consent" });
  const u = new URL(to);
  assert.equal(u.origin, "https://192.168.1.2:8443");
  assert.equal(u.pathname, "/oauth/callback");
  assert.deepEqual([...u.searchParams.keys()].sort(), ["code", "state"]);
  assert.equal(u.searchParams.get("code"), "4/0Ab-code");
  assert.equal(u.searchParams.get("state"), s);
});

test("a workspace behind its own domain", () => {
  const u = new URL(call({ code: "c", state: state("https://ia.example.org") }));
  assert.equal(u.origin, "https://ia.example.org");
  assert.equal(u.pathname, "/oauth/callback");
});

test("a refusal from Google is passed on, without its description", () => {
  const u = new URL(call({ error: "access_denied", error_description: "<script>", state: state("https://eriphe.tail1234.ts.net") }));
  assert.equal(u.origin, "https://eriphe.tail1234.ts.net");
  assert.deepEqual([...u.searchParams.keys()].sort(), ["error", "state"]);
  assert.equal(u.searchParams.get("error"), "access_denied");
});

test("the workspace origins sent back to (origins.json, as core's bounceOrigin)", () => {
  for (const w of ORIGINS.allowed) {
    assert.ok(allowedOrigin(w), w);
    assert.ok(call({ code: "c", state: state(w) })?.startsWith(`${w}/oauth/callback?`), w);
  }
});

test("no other redirect (origins.json, as core's bounceOrigin)", () => {
  for (const w of ORIGINS.refused) {
    assert.equal(allowedOrigin(w), false, w);
    assert.equal(call({ code: "c", state: state(w) }), null, w);
  }
});

test("a malformed, expired or foreign state is refused", () => {
  const good = state("https://192.168.1.2:8443");
  const [v, payload, sig] = good.split(".");
  for (const [name, s] of [
    ["missing", ""],
    ["an MCP SDK state", "Zm9vYmFy"],
    ["another version", `e2.${payload}.${sig}`],
    ["no MAC", `${v}.${payload}`],
    ["unreadable payload", `e1.!!!.${sig}`],
    ["bad base64", `e1.A.${sig}`],
    ["not JSON", `e1.${Buffer.from("w=https://x").toString("base64url")}.${sig}`],
    ["not UTF-8", `e1.${Buffer.from([0xff, 0xfe, 0x7b]).toString("base64url")}.${sig}`],
    ["not an object", `e1.${Buffer.from("null").toString("base64url")}.${sig}`],
    ["expired", state("https://192.168.1.2:8443", NOW / 1000 - 1)],
    ["too far ahead", state("https://192.168.1.2:8443", NOW / 1000 + 7200)],
    ["expiry not a number", state("https://192.168.1.2:8443", "9999999999")],
    ["short flow id", state("https://192.168.1.2:8443", undefined, "abc")],
    ["too long", `${good}${"A".repeat(1100)}`],
  ]) {
    assert.equal(call({ code: "c", state: s }), null, name);
  }
  assert.equal(call({ state: good }), null, "neither code nor error");
  assert.equal(call({ state: good, code: "c", error: "access_denied" }), null, "code and error");
  assert.equal(call({ state: good, error: "<b>" }), null, "odd error");
  assert.equal(call({ state: good, code: "" }), null, "empty code");
  assert.equal(call({ state: good, code: "a\nb" }), null, "code with a newline");
  assert.equal(call(`code=c&state=${encodeURIComponent(good)}&state=${encodeURIComponent(state("https://evil.example.org"))}`), null, "state twice");
  assert.equal(call(`code=c&code=d&state=${encodeURIComponent(good)}`), null, "code twice");
});

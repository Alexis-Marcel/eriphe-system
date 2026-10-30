// The Google sign-in relay of the Eriphe app (core/cloud.go, core/README.md "Google Drive").
//
// The Eriphe app registered at Google has a single redirect, this page, and each installation has
// its own address: like Home Assistant's my.home-assistant.io, the page only sends the browser on
// to the workspace named in the state. It never exchanges the code and keeps nothing; the
// workspace checks the state's MAC (a key only it has), its expiry, the browser's flow cookie, and
// exchanges the code with its PKCE verifier.
//
// The page cannot check the MAC (a key shared by every installation would be public): it holds
// the state to its shape and the origin to the rule of bounceOrigin in eriphe-core. An origin
// only (no path, query or credentials), exactly as a browser writes it: https to any host name or
// address, http only to a loopback or private address or localhost. Only code and state (or
// error) are forwarded, always to /oauth/callback.

const VERSION = "e1";
const MAX_STATE = 1024;
const MAX_AHEAD_S = 3600; // a state expires within the hour at most (eriphe-core: 15 min)
const HOST = /^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)*$/;
const NUMERIC = /^(0x[0-9a-f]*|[0-9]+)$/;
const IPV4 = /^(\d{1,3})\.(\d{1,3})\.(\d{1,3})\.(\d{1,3})$/;
const B64URL = /^[A-Za-z0-9_-]+$/;

/**
 * Where to send the browser back from Google, or null.
 * @param {string} href the page's address, with Google's query
 * @param {number} now milliseconds
 * @returns {string | null}
 */
export function relay(href, now = Date.now()) {
  let q;
  try {
    q = new URL(href).searchParams;
  } catch {
    return null;
  }
  for (const k of ["state", "code", "error"]) {
    if (q.getAll(k).length > 1) return null;
  }
  const state = q.get("state") ?? "";
  const code = q.get("code");
  const error = q.get("error");
  const origin = stateOrigin(state, now);
  if (!origin || (code === null) === (error === null)) return null;
  const out = new URLSearchParams();
  if (code !== null) {
    if (code === "" || code.length > 2048 || /[\s\x00-\x1f\x7f]/.test(code)) return null;
    out.set("code", code);
  } else {
    if (!/^[a-z_]{1,64}$/.test(error)) return null;
    out.set("error", error);
  }
  out.set("state", state);
  return `${origin}/oauth/callback?${out}`;
}

/** The origin of a well-formed state, still valid, that names a workspace; otherwise null. */
export function stateOrigin(state, now = Date.now()) {
  if (state.length > MAX_STATE) return null;
  const parts = state.split(".");
  if (parts.length !== 3 || parts[0] !== VERSION || !B64URL.test(parts[1]) || !B64URL.test(parts[2])) return null;
  let p;
  try {
    const b64 = parts[1].replace(/-/g, "+").replace(/_/g, "/");
    const bytes = Uint8Array.from(atob(b64 + "=".repeat((4 - (b64.length % 4)) % 4)), (c) => c.charCodeAt(0));
    p = JSON.parse(new TextDecoder("utf-8", { fatal: true }).decode(bytes));
  } catch {
    return null;
  }
  if (typeof p !== "object" || p === null) return null;
  const { w, n, x } = p;
  if (typeof w !== "string" || typeof n !== "string" || typeof x !== "number") return null;
  if (!/^[A-Za-z0-9_-]{8,64}$/.test(n)) return null;
  const s = Math.floor(now / 1000);
  if (x < s || x > s + MAX_AHEAD_S) return null;
  return allowedOrigin(w) ? w : null;
}

/** A workspace origin the relay sends back to (bounceOrigin in core/cloud.go). */
export function allowedOrigin(w) {
  if (typeof w !== "string" || w.length > 255 || w !== w.toLowerCase()) return false;
  let u;
  try {
    u = new URL(w);
  } catch {
    return false;
  }
  // An exact origin: no path, query or credentials, no default port written, no name the parser
  // rewrites as an address (0x7f000001).
  if (u.origin !== w || u.username || u.password || (u.port !== "" && !/^[1-9][0-9]{0,4}$/.test(u.port))) return false;
  const host = u.hostname;
  const v6 = host.startsWith("[");
  const v4 = IPV4.exec(host);
  // ::ffff:a.b.c.d, which eriphe-core reads as an IPv4 address.
  if (v6 && /^\[::ffff:[0-9a-f]{1,4}:[0-9a-f]{1,4}\]$/.test(host)) return false;
  if (u.protocol === "http:") return host === "localhost" || (v4 ? private4(v4) : v6 && private6(host.slice(1, -1)));
  if (u.protocol !== "https:") return false;
  if (v4 || v6) return true;
  const labels = host.split(".");
  return HOST.test(host) && !NUMERIC.test(labels[labels.length - 1]);
}

function private4(m) {
  const [a, b] = [Number(m[1]), Number(m[2])];
  return (
    a === 127 || a === 10 || (a === 172 && b >= 16 && b <= 31) || (a === 192 && b === 168) ||
    (a === 169 && b === 254) || (a === 100 && b >= 64 && b <= 127) // link-local, shared (Tailscale)
  );
}

function private6(h) {
  if (h === "::1") return true;
  // fc00::/7 (Tailscale's fd7a:115c:a1e0::/48 among them) and fe80::/10
  return /^f[cd][0-9a-f]{0,2}:/.test(h) || /^fe[89ab][0-9a-f]?:/.test(h);
}

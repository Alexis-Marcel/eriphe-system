import { afterEach, describe, expect, it, vi } from "vitest";
import * as data from "./data";
import { keysOf } from "./engine";
import { keyLabel } from "./Espace";
import { makeT } from "./i18n";
import strings from "./strings.json";
import type { Obj } from "./types";

// The ⋯ menu's remarks become eriphe-core's rules: keys in its grammar,
// labels in words, and a POST to /api/rules with the CSRF token.
describe("rules", () => {
  afterEach(() => vi.unstubAllGlobals());

  it("labels every key the menu offers, in both languages", () => {
    const objs: [Obj, string?][] = [
      [{ ref: "mail:1", kind: "mail", title: "", mail: { id: "1", account: "", folder: "", from: "Lou <lou@example.org>", subject: "", date: "", unread: false, body: "" } }, "skill:mail"],
      [{ ref: "alert:1", kind: "alert", title: "" }, "skill:backup"],
      [{ ref: "task:t1", kind: "task", title: "", task: { id: "t1", title: "", agent: "sonnet", state: "running", created: "", updated: "" } }],
    ];
    for (const lang of ["fr", "en"] as const) {
      const t = makeT(strings[lang]);
      for (const [o, source] of objs) {
        for (const key of keysOf(o, source)) {
          expect(keyLabel(t, key), key).not.toMatch(/espace\.|%s/);
        }
      }
    }
    const t = makeT(strings.fr);
    expect(keyLabel(t, "mail:from=example.org")).toBe("les mails de example.org");
    expect(keyLabel(t, "alert:source=backup")).toBe("les alertes de backup");
    expect(keyLabel(t, "task:agent=sonnet")).toBe("les tâches (sonnet)");
    expect(keyLabel(t, "mail")).toBe("tous les mails");
  });

  it("posts a remark to /api/rules", async () => {
    const fetch = vi.fn(async () => new Response(JSON.stringify({ id: 4, key: "mail:from=example.org", delta: -5, label: "x", at: "" }), { status: 201 }));
    vi.stubGlobal("fetch", fetch);
    vi.stubGlobal("location", { assign: vi.fn() });
    await data.load({ lang: "fr", name: "Eriphe", csrf: "tok", strings: {}, live: false });
    const rule = await data.addRule({ key: "mail:from=example.org", delta: -5, label: "les mails de example.org" });
    expect(rule?.id).toBe(4);
    const [url, init] = fetch.mock.calls[0] as unknown as [string, RequestInit];
    expect(url).toBe("/api/rules");
    expect(init.method).toBe("POST");
    expect((init.headers as Record<string, string>)["X-CSRF-Token"]).toBe("tok");
    expect(JSON.parse(init.body as string)).toEqual({ key: "mail:from=example.org", delta: -5, label: "les mails de example.org" });

    fetch.mockImplementationOnce(async () => new Response("{}", { status: 400 }));
    expect(await data.addRule({ key: "nope", delta: 1, label: "" })).toBeNull();
  });
});

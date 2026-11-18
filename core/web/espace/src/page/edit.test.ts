import { describe, expect, it } from "vitest";
import type { NodeFile } from "../data/api";
import { dirty, fromFile, keepMine, read, saved, saving, takeNewer, typed } from "./edit";

const file = (version: number, body: string): NodeFile => ({ md: "", body, version, hash: `h${version}`, last: version });

describe("the .md tab's edit", () => {
  it("saves on the version it was made on, which then is its base", () => {
    let e = typed(fromFile(file(2, "a\n")), "a\nb\n");
    expect(dirty(e)).toBe(true);
    e = saved(saving(e), "a\nb\n", { ok: true, version: 3, hash: "h3" });
    expect(e).toMatchObject({ status: "saved", base: { version: 3, hash: "h3", body: "a\nb\n" } });
    expect(dirty(e)).toBe(false);
    // Its own version, read again, changes nothing.
    expect(read(e, { ...file(3, "a\nb\n") })).toBe(e);
  });

  it("takes a newer version while the user changed nothing, and keeps their text otherwise", () => {
    const clean = fromFile(file(2, "a\n"));
    expect(read(clean, file(3, "c\n"))).toMatchObject({ text: "c\n", base: { version: 3 } });
    const mine = typed(clean, "mine\n");
    expect(read(mine, file(3, "c\n"))).toBe(mine);
  });

  it("shows a conflict with the newer text, and writes nothing until the user chooses", () => {
    const e = saved(saving(typed(fromFile(file(2, "a\n")), "mine\n")), "mine\n", { ok: false, conflict: { version: 4, hash: "h4", body: "theirs\n" } });
    expect(e).toMatchObject({ status: "conflict", base: { version: 2 }, text: "mine\n", newer: { version: 4, body: "theirs\n" } });
    expect(typed(e, "mine, more\n").status).toBe("conflict");
    expect(takeNewer(e)).toMatchObject({ status: "idle", text: "theirs\n", base: { version: 4, hash: "h4" } });
    const kept = keepMine(e);
    expect(kept).toMatchObject({ status: "idle", text: "mine\n", base: { version: 4, hash: "h4" } });
    expect(dirty(kept)).toBe(true);
  });

  it("says why a save was refused", () => {
    const e = saved(saving(typed(fromFile(file(1, "")), "x")), "x", { ok: false, why: "a secret" });
    expect(e).toMatchObject({ status: "failed", why: "a secret", text: "x" });
  });
});

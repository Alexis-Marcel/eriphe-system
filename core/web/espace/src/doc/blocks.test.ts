// @vitest-environment jsdom
import { describe, expect, it } from "vitest";
import { createAdapter } from "../a2ui/adapter";
import type { NodePage } from "../data/api";
import { messages, pieces, sourceMessage, surfaceOf } from "./blocks";
import golden from "./page.golden.json";

// The page as eriphe-core sends it (page.golden.json, written by
// TestNodePageGolden): its text, then a live block.
const page = golden as unknown as NodePage;

describe("a node's page", () => {
  it("reads as its text and its blocks, in order", () => {
    const ps = pieces(page);
    expect(ps.map((p) => p.kind)).toEqual(["text", "block"]);
    expect(ps[0]).toMatchObject({ kind: "text", text: expect.stringContaining("Trois titres.") });
    expect(ps[1]).toMatchObject({ kind: "block", surface: surfaceOf("plan1", "titles") });
  });

  it("makes a surface of each block, with its data and its sources' values, which the renderer accepts", () => {
    const a = createAdapter("fr");
    const errors: unknown[] = [];
    a.onError((e) => errors.push(e));
    a.applyMessages(messages(page));
    expect(errors).toEqual([]);
    const s = a.surfaces();
    expect(s.map((x) => x.id)).toEqual(["plan1--titles"]);
    expect(s[0].dataModel.get("/ledger/accounts/1/name")).toBe("Titre 2");
    expect(s[0].dataModel.get("/heading")).toBe("Titres");
    // a source's new value goes where the block reads it
    a.applyMessages([sourceMessage("plan1", "titles", "ledger", { accounts: [{ name: "Autre" }] })]);
    expect(s[0].dataModel.get("/ledger/accounts/0/name")).toBe("Autre");
    expect(errors).toEqual([]);
  });

  it("marks a block someone else's words may have written, and shows a broken one as broken", () => {
    const ext: NodePage = { ...page, parts: [{ block: { ...page.parts[1].block!, external: true } }, { block: { name: "bad", components: null, data: null, error: "not JSON" } }] };
    const [m] = messages(ext);
    expect(m).toMatchObject({ createSurface: { metadata: { extensions: { eriphe: { tainted: true } } } } });
    expect(pieces(ext)[1]).toEqual({ kind: "broken", name: "bad", error: "not JSON" });
  });
});

import { describe, expect, it } from "vitest";
import { readPage } from "./read";

// The espace reads a page as eriphe-core does (core/nodefile/md.go): the
// same tree, field for field, for every page of core/nodefile/testdata/pages
// (written by Go's reader: go test ./nodefile -run TestPageFixtures -update).
const pages = import.meta.glob<string>("../../../../nodefile/testdata/pages/*.md", { query: "?raw", import: "default", eager: true });
const trees = import.meta.glob<{ page: unknown }>("../../../../nodefile/testdata/pages/*.json", { import: "default", eager: true });

describe("readPage", () => {
  const names = Object.keys(pages);
  it("has the fixture pages", () => expect(names.length).toBeGreaterThanOrEqual(10));
  for (const md of names) {
    it(md.split("/").pop()!, () => {
      const want = trees[md.replace(/\.md$/, ".json")];
      expect(want, "its JSON").toBeTruthy();
      expect(JSON.parse(JSON.stringify(readPage(pages[md])))).toEqual(want.page);
    });
  }

  it("reads CRLF, tabs and non-breaking spaces as Go does", () => {
    const p = readPage("- [ ] un\r\n\t- détail\r\n- pas un élément\r\n");
    expect(p.blocks[0].items?.[0].detail?.[0].kind).toBe("list");
    expect(p.blocks[1]).toEqual({ kind: "paragraph", line: 3, end: 3, text: "- pas un élément" });
  });
});

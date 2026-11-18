import { describe, expect, it } from "vitest";
import { knownSections, type KnownFile } from "./api";

const file = (path: string, section: string): KnownFile => ({ path, section, title: path, lines: [{ id: 1, text: "x" }] });

describe("knownSections", () => {
  it("groups the mirror's files by section, in the order they came", () => {
    const got = knownSections([file("profil.md", "profile"), file("personnes/sam.md", "people"), file("personnes/lou.md", "people"), file("journal/2026-11-18.md", "journal")]);
    expect(got.map((s) => [s.section, s.files.map((f) => f.path)])).toEqual([
      ["profile", ["profil.md"]],
      ["people", ["personnes/sam.md", "personnes/lou.md"]],
      ["journal", ["journal/2026-11-18.md"]],
    ]);
  });
  it("is empty for nothing known", () => {
    expect(knownSections([])).toEqual([]);
  });
});

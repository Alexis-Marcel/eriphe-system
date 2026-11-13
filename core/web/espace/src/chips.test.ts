import { describe, expect, it } from "vitest";
import { chipLabel, shownChips, surfaceTitle } from "./chips";

// The chips' words: never a raw id, and those that say the same once,
// with their count.
describe("chips", () => {
  const titles: Record<string, string> = { "q-card-322": "Souvenirs à garder" };
  const titleOf = (id: string) => titles[id];

  it("never show a raw id: the surface's title, else a generic word", () => {
    const got = shownChips(
      [
        { id: "news", title: "Nouvelles" },
        { id: "q-card-322", title: "q-card-322" },
        { id: "s-9", title: "s-9" },
        { id: "s-10", title: "s-10" },
      ],
      titleOf,
      "Sans titre",
    );
    // Two untitled chips are not the same: each stays, to be opened.
    expect(got.map(chipLabel)).toEqual(["Nouvelles", "Souvenirs à garder", "Sans titre", "Sans titre"]);
  });

  it("collapse chips that say the same into one, with their count", () => {
    const twelve = Array.from({ length: 12 }, (_, i) => ({ id: `q-memory-${i}`, title: "Souvenirs à garder" }));
    const got = shownChips([{ id: "news", title: "Nouvelles" }, ...twelve, { id: "old", title: "Souvenirs à garder", closed: true }], titleOf, "Sans titre");
    expect(got.map(chipLabel)).toEqual(["Nouvelles", "Souvenirs à garder · 12", "Souvenirs à garder"]);
    // The first stands for them: a click opens it.
    expect(got[1].id).toBe("q-memory-0");
    expect(got[1].ids).toHaveLength(12);
    expect(got[2].closed).toBe(true);
  });

  it("read a surface's title from its metadata", () => {
    expect(surfaceTitle({ metadata: { extensions: { eriphe: { title: " Agenda " } } } })).toBe("Agenda");
    expect(surfaceTitle({ metadata: {} })).toBeUndefined();
    expect(surfaceTitle(undefined)).toBeUndefined();
  });
});

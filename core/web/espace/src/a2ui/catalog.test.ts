// @vitest-environment jsdom
import { describe, expect, it } from "vitest";
import served from "../../../../a2ui/catalog_v1.json";
import { CATALOG, components } from "./catalog";

// The page implements the catalog eriphe-core serves (core/a2ui/catalog_v1.json):
// the same id, and for each component it writes, a component of that name
// whose every property the catalog declares.
describe("catalog", () => {
  it("is the one eriphe-core serves", () => {
    expect(CATALOG.id).toBe(served.catalogId);
  });

  const declared = served.components as Record<string, { properties: Record<string, unknown> }>;
  for (const c of components) {
    it(`has ${c.name} as core declares it`, () => {
      expect(declared[c.name], c.name).toBeDefined();
      const shape = (c.schema as unknown as { shape: Record<string, unknown> }).shape;
      for (const prop of Object.keys(shape)) expect(declared[c.name].properties, `${c.name}.${prop}`).toHaveProperty(prop);
    });
  }
});

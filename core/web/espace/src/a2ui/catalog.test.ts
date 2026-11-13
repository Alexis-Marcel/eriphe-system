// @vitest-environment jsdom
import { describe, expect, it } from "vitest";
import served from "../../../../a2ui/catalog_v1.json";
import { CATALOG, catalog, components } from "./catalog";
import { ICON_NAMES } from "./icons";

// The page implements the catalog eriphe-core serves (core/a2ui/catalog_v1.json):
// the same id, every component it declares, each with exactly its
// properties and the same allowed values, and every function.

type Prop = { enum?: string[]; $ref?: string };
const declared = served.components as unknown as Record<string, { properties: Record<string, Prop> }>;
const defs = served.$defs as unknown as Record<string, Prop & { oneOf?: Prop[] }>;

type Zod = { _def: { typeName: string; innerType?: Zod; values?: string[]; options?: Zod[] } };

// The values a zod schema allows, when it is an enum (or an optional one,
// or a union with one).
function enumOf(s: Zod | undefined): string[] | undefined {
  while (s && s._def.typeName === "ZodOptional") s = s._def.innerType;
  if (!s) return undefined;
  if (s._def.typeName === "ZodEnum") return s._def.values;
  if (s._def.typeName === "ZodUnion") for (const o of s._def.options ?? []) if (enumOf(o)) return enumOf(o);
  return undefined;
}

// The values the catalog allows for a property, following a local $ref.
function declaredEnum(p: Prop): string[] | undefined {
  if (p.enum) return p.enum;
  const ref = p.$ref?.match(/^#\/\$defs\/(\w+)$/)?.[1];
  if (!ref) return undefined;
  const d = defs[ref];
  return d.enum ?? d.oneOf?.find((o) => o.enum)?.enum;
}

describe("catalog", () => {
  it("is the one eriphe-core serves", () => {
    expect(CATALOG.id).toBe(served.catalogId);
  });

  it("implements every component core declares, and no other", () => {
    expect(components.map((c) => c.name).sort()).toEqual(Object.keys(declared).sort());
    expect(components).toHaveLength(25);
  });

  for (const c of components) {
    it(`has ${c.name} as core declares it`, () => {
      expect(declared[c.name], c.name).toBeDefined();
      const shape = (c.schema as unknown as { shape: Record<string, Zod> }).shape;
      const props = Object.keys(declared[c.name].properties).filter((p) => p !== "component");
      expect(Object.keys(shape).sort(), c.name).toEqual(props.sort());
      for (const p of props) {
        const want = declaredEnum(declared[c.name].properties[p]);
        if (want) expect(enumOf(shape[p]), `${c.name}.${p}`).toEqual(want);
      }
    });
  }

  it("draws every icon core names", () => {
    expect([...ICON_NAMES].sort()).toEqual([...(declaredEnum({ $ref: "#/$defs/IconName" }) ?? [])].sort());
  });

  it("has every function core declares", () => {
    for (const lang of ["fr", "en"] as const) {
      expect([...catalog(lang).functions.keys()].sort()).toEqual(Object.keys(served.functions).sort());
    }
  });
});

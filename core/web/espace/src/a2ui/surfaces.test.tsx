// @vitest-environment jsdom
import { act, StrictMode } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { createAdapter, type ActionMessage, type Adapter, type ErrorMessage, type Message } from "./adapter";
import { CATALOG } from "./catalog";
import { surfaceFixtures } from "./fixtures";
import { Surfaces } from "./Surfaces";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

let el: HTMLDivElement;
let root: Root;
let adapter: Adapter;
let errors: ErrorMessage[];

beforeEach(async () => {
  el = document.createElement("div");
  document.body.append(el);
  adapter = createAdapter();
  errors = [];
  adapter.onError((e) => errors.push(e));
  root = createRoot(el);
  await act(async () =>
    root.render(
      <StrictMode>
        <Surfaces adapter={adapter} />
      </StrictMode>,
    ),
  );
});

afterEach(async () => {
  await act(async () => root.unmount());
  adapter.dispose();
  el.remove();
});

const apply = (msgs: Message[] | unknown[]) => act(async () => adapter.applyMessages(msgs));
const texts = (sel: string) => [...el.querySelectorAll(sel)].map((n) => n.textContent);

const surface = (components: unknown[], dataModel: unknown = {}): Message => ({
  version: "1.0",
  createSurface: { surfaceId: "s1", catalogId: CATALOG.id, components, dataModel },
});

// An error message as A2UI v1.0's renderer_to_agent.json allows it: a
// validation error has exactly code, surfaceId, path and message; another
// has code and message, and a surfaceId.
function fitsProtocol(m: ErrorMessage): boolean {
  if (Object.keys(m).sort().join() !== "error,version" || m.version !== "1.0") return false;
  const e = m.error as Record<string, unknown>;
  const all = (ks: string[]) => ks.every((k) => typeof e[k] === "string" && e[k] !== "");
  if (["VALIDATION_FAILED", "UNALLOWED_PARENT", "UNALLOWED_CHILD"].includes(String(e.code))) {
    return Object.keys(e).sort().join() === "code,message,path,surfaceId" && all(["code", "message", "path", "surfaceId"]);
  }
  return all(["code", "message", "surfaceId"]) && !("functionCallId" in e);
}

describe("A2UI surfaces", () => {
  it("render from messages", async () => {
    await apply([
      surface(
        [
          { id: "root", component: "Column", children: ["hello", "row"] },
          { id: "hello", component: "Text", variant: "title", text: "Bonjour" },
          { id: "row", component: "Row", justify: "spaceBetween", children: ["a", "b"] },
          { id: "a", component: "Text", text: { "@path": "/a" } },
          { id: "b", component: "Text", text: "b" },
        ],
        { a: "lié" },
      ),
    ]);
    expect(el.querySelector('[data-surface="s1"] > .a2-column')).not.toBeNull();
    expect(el.querySelector(".a2-text-title")?.textContent).toBe("Bonjour");
    expect(el.querySelector(".a2-row.a2-justify-spaceBetween")).not.toBeNull();
    expect(texts(".a2-row .a2-text")).toEqual(["lié", "b"]);
    expect(errors).toEqual([]);
  });

  it("update one data path", async () => {
    await apply(surfaceFixtures("fr"));
    const before = el.querySelector(".a2-text-title");
    await apply([{ version: "1.0", updateDataModel: { surfaceId: "sample-today", path: "/items/1/label", value: "Autre chose" } }]);
    expect(texts(".a2-list li")).toEqual(["Relire le devis de Sam", "Autre chose", "Répondre à l'école"]);
    // What did not change stays the same element.
    expect(el.querySelector(".a2-text-title")).toBe(before);
  });

  it("repeat a template over a list, and say when it is empty", async () => {
    await apply(surfaceFixtures("en"));
    expect(texts(".a2-list li")).toEqual(["Read Sam's quote", "Prepare the 2 pm meeting", "Answer the school"]);
    await apply([{ version: "1.0", updateDataModel: { surfaceId: "sample-today", path: "/items", value: [{ label: "One" }] } }]);
    expect(texts(".a2-list li")).toEqual(["One"]);
    await apply([{ version: "1.0", updateDataModel: { surfaceId: "sample-today", path: "/items", value: [] } }]);
    expect(el.querySelector(".a2-list")).toBeNull();
    expect(el.querySelector(".a2-empty")?.textContent).toBe("Nothing to do");
  });

  it("upsert components by id and delete a surface", async () => {
    await apply(surfaceFixtures("en"));
    await apply([{ version: "1.0", updateComponents: { surfaceId: "sample-today", components: [{ id: "title", component: "Text", text: "Changed" }] } }]);
    expect(el.querySelector(".a2-column > .a2-text")?.textContent).toBe("Changed");
    await apply([{ version: "1.0", deleteSurface: { surfaceId: "sample-today" } }]);
    expect(el.querySelector(".a2-surface")).toBeNull();
  });

  it("send an action to the adapter", async () => {
    const got: ActionMessage[] = [];
    adapter.onAction((a) => got.push(a));
    await apply(surfaceFixtures("fr"));
    await act(async () => (el.querySelector("button.a2-button") as HTMLButtonElement).click());
    expect(got).toHaveLength(1);
    expect(got[0].version).toBe("1.0");
    expect(got[0].action).toMatchObject({
      name: "ask",
      surfaceId: "sample-today",
      sourceComponentId: "ask",
    });
    // An ask carries its author's words, and nothing else: core adds the rest.
    expect(got[0].action.context).toEqual({ text: "Résume ma journée en trois lignes" });
    expect(Date.parse(got[0].action.timestamp)).not.toBeNaN();
  });

  it("report what it refuses as an error, and keep the rest", async () => {
    await apply([
      surface([{ id: "root", component: "Shiny", text: "?" }]),
      { version: "1.0", createSurface: { surfaceId: "s2", catalogId: CATALOG.id, components: [] } },
      { version: "1.0", createSurface: { surfaceId: "s3", catalogId: "urn:example:other", components: [{ id: "root", component: "Text", text: "?" }] } },
      { version: "1.0", updateDataModel: { surfaceId: "nowhere", path: "/a", value: 1 } },
      ...surfaceFixtures("en"),
    ]);
    // One error per message refused, each told once (a component of an
    // unknown type is found when the surface renders, after the others).
    expect(errors.map((e) => [e.error.surfaceId, e.error.code])).toEqual([
      ["s2", "VALIDATION_FAILED"],
      ["s3", "CATALOG_NOT_FOUND"],
      ["nowhere", "INTEGRITY_ERROR"],
      ["s1", "UNKNOWN_COMPONENT_TYPE"],
    ]);
    for (const e of errors) expect(fitsProtocol(e), JSON.stringify(e)).toBe(true);
    expect(el.querySelector('[data-surface="sample-today"] .a2-list')).not.toBeNull();
  });

  it("set no style attribute and add no <style>", async () => {
    const before = document.querySelectorAll("style").length;
    await apply(surfaceFixtures("fr"));
    await apply([{ version: "1.0", updateDataModel: { surfaceId: "sample-today", path: "/title", value: "x" } }]);
    expect(el.querySelectorAll("*").length).toBeGreaterThan(5);
    expect(document.querySelectorAll("[style]")).toHaveLength(0);
    expect(document.querySelectorAll("style")).toHaveLength(before);
    expect(document.adoptedStyleSheets ?? []).toHaveLength(0);
    // Every element is our markup: no custom element wraps a component.
    expect([...el.querySelectorAll("*")].filter((n) => n.tagName.includes("-"))).toEqual([]);
  });
});

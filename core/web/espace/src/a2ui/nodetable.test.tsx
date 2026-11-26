// @vitest-environment jsdom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import type { GraphNode, NodePage } from "../data/api";
import { Props } from "../doc/Props";
import { makeT, WordsContext } from "../i18n";
import strings from "../strings.json";
import { createAdapter, type Adapter } from "./adapter";
import { CATALOG } from "./catalog";
import { ListsContext, type Lists } from "./lists";
import { Surfaces } from "./Surfaces";

// A block whose source is node_find (#743): a Table of the nodes it found,
// a list's values written in words; a row whose id is a node of the graph
// shows its title as the graph does, marked when it may be someone else's
// words (node_find leaves such a title out), and its first cell opens its
// page. A node's page shows its tags and properties, read-only.

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const node = (id: string, title: string, titleExternal = false): GraphNode => ({
  id, title, titleExternal, weight: 0.6, status: "idle", created: "2026-11-01T09:00:00Z", updated: "2026-11-01T09:00:00Z", version: 1,
});
const graph: Record<string, GraphNode> = { a1: node("a1", "Étude sur le sommeil"), b2: node("b2", "Un mail reçu", true) };

let el: HTMLDivElement;
let root: Root;
let adapter: Adapter;
let opened: string[];

async function mount(ui: React.ReactNode) {
  root = createRoot(el);
  await act(async () => root.render(<WordsContext.Provider value={{ t: makeT(strings.fr), lang: "fr" }}>{ui}</WordsContext.Provider>));
}

beforeEach(() => {
  el = document.createElement("div");
  document.body.append(el);
  opened = [];
  adapter = createAdapter("fr");
});

afterEach(() => {
  act(() => root.unmount());
  adapter.dispose();
  el.remove();
});

describe("a Table of nodes", () => {
  it("shows each node, opens it, writes lists in words", async () => {
    const lists: Lists = { list: () => undefined, node: (id) => graph[id], openNode: (id) => opened.push(id) };
    await mount(
      <ListsContext.Provider value={lists}>
        <Surfaces adapter={adapter} />
      </ListsContext.Provider>,
    );
    await act(async () =>
      adapter.applyMessages([
        {
          version: "1.0" as const,
          createSurface: {
            surfaceId: "p1--etudes",
            catalogId: CATALOG.id,
            components: [
              {
                id: "root", component: "Table", rows: { "@path": "/etudes/nodes" },
                columns: [{ key: "title", label: "Étude" }, { key: "pages", label: "Pages", kind: "number" }, { key: "tags", label: "Tags" }],
              },
            ],
            dataModel: {
              etudes: {
                nodes: [
                  { id: "a1", title: "Étude sur le sommeil", weight: 0.6, status: "idle", pages: 1200, tags: ["etude", "sante"] },
                  { id: "b2", title_external: true, weight: 0.6, status: "idle", withheld: true },
                ],
              },
            },
          },
        },
      ]),
    );
    const rows = [...el.querySelectorAll("table.a2-table tbody tr")];
    expect(rows).toHaveLength(2);
    expect(rows[0].textContent).toContain("Étude sur le sommeil");
    expect(rows[0].textContent).toContain("etude, sante");
    expect(rows[0].textContent).toMatch(/1\s?200/);
    // The title node_find left out: the graph's, marked.
    expect(rows[1].querySelector(".title-ext")?.textContent).toContain("Un mail reçu");
    await act(async () => (rows[1].querySelector(".a2-row-open") as HTMLElement).click());
    expect(opened).toEqual(["b2"]);
  });
});

describe("a node's properties", () => {
  it("shows its tags and properties, nothing when it has none", async () => {
    const page = {
      node: graph.a1, version: 1, versions: [], body: "", parts: [], cards: [],
      tags: ["etude/sante"],
      props: [{ key: "pages", value: 1200, kind: "number" }, { key: "auteurs", value: ["Sam", "[[Lina]]"], kind: "text" }],
    } as NodePage;
    await mount(<Props page={page} />);
    const dl = el.querySelector("dl.pg-props");
    expect(dl?.getAttribute("aria-label")).toBe("Ses propriétés");
    expect(el.querySelector(".pg-tag")?.textContent).toBe("#etude/sante");
    expect([...el.querySelectorAll(".pg-prop")].map((p) => p.textContent)).toEqual([expect.stringMatching(/^pages1\s?200$/), "auteursSam, [[Lina]]"]);
    act(() => root.unmount());
    await mount(<Props page={{ ...page, tags: [], props: [] }} />);
    expect(el.querySelector(".pg-props")).toBeNull();
  });

  it("shows a photo kept with it as its picture, a PDF as a link", async () => {
    const page = {
      node: graph.a1, version: 1, versions: [], body: "", parts: [], cards: [],
      tags: ["vêtement"],
      props: [
        { key: "photo", value: "media:med_a.jpg", kind: "text", media: [{ src: "/espace/media?u=media%3Aa1%2Fmed_a.jpg&s=x", image: true }] },
        { key: "notice", value: "media:med_b.pdf", kind: "text", media: [{ src: "/espace/media?u=media%3Aa1%2Fmed_b.pdf&s=y", image: false }] },
        { key: "couleur", value: "bleu", kind: "text" },
      ],
    } as NodePage;
    await mount(<Props page={page} />);
    const img = el.querySelector<HTMLImageElement>("img.pg-picture");
    expect(img?.getAttribute("src")).toBe("/espace/media?u=media%3Aa1%2Fmed_a.jpg&s=x");
    expect(img?.alt).toBe("photo");
    const file = el.querySelector<HTMLAnchorElement>("a.pg-file");
    expect(file?.getAttribute("href")).toBe("/espace/media?u=media%3Aa1%2Fmed_b.pdf&s=y");
    expect(file?.getAttribute("download")).toBe("notice.pdf");
    expect(file?.textContent).toBe("notice · télécharger");
    // Its other properties as before; the media: value never as text.
    expect([...el.querySelectorAll(".pg-prop")].map((p) => p.textContent)).toEqual(["couleurbleu"]);
    expect(el.textContent).not.toContain("media:");
  });
});

describe("a Table of nodes with their photo", () => {
  it("shows each node's photo as a thumbnail, a PDF as a link", async () => {
    const lists: Lists = { list: () => undefined, node: (id) => graph[id], openNode: (id) => opened.push(id) };
    await mount(
      <ListsContext.Provider value={lists}>
        <Surfaces adapter={adapter} />
      </ListsContext.Provider>,
    );
    await act(async () =>
      adapter.applyMessages([
        {
          version: "1.0" as const,
          createSurface: {
            surfaceId: "p1--garde",
            catalogId: CATALOG.id,
            components: [
              {
                id: "root", component: "Table", rows: { "@path": "/garde/nodes" },
                columns: [{ key: "title", label: "Vêtement" }, { key: "photo", label: "Photo" }],
              },
            ],
            dataModel: {
              garde: {
                nodes: [
                  { id: "a1", title: "Chemise bleue", weight: 0.6, status: "idle", photo: "/espace/media?u=media%3Aa1%2Fmed_a.jpg&s=x" },
                  { id: "b2", title: "Notice", weight: 0.6, status: "idle", photo: ["/espace/media?u=media%3Ab2%2Fmed_b.pdf&s=y"] },
                  { id: "c3", title: "Sans photo", weight: 0.6, status: "idle", photo: "media:med_c.jpg" },
                ],
              },
            },
          },
        },
      ]),
    );
    const rows = [...el.querySelectorAll("table.a2-table tbody tr")];
    expect(rows).toHaveLength(3);
    const thumb = rows[0].querySelector<HTMLImageElement>("img.a2-thumb");
    expect(thumb?.getAttribute("src")).toBe("/espace/media?u=media%3Aa1%2Fmed_a.jpg&s=x");
    expect(thumb?.getAttribute("loading")).toBe("lazy");
    expect(rows[1].querySelector("img")).toBeNull();
    expect(rows[1].querySelector("a.a2-file")?.getAttribute("href")).toBe("/espace/media?u=media%3Ab2%2Fmed_b.pdf&s=y");
    // A value eriphe-core did not sign is text, never loaded.
    expect(rows[2].querySelector("img")).toBeNull();
  });
});

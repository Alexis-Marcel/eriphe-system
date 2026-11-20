// @vitest-environment jsdom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import type { Opened, PickAction, PickList } from "../data/api";
import { makeT, WordsContext } from "../i18n";
import strings from "../strings.json";
import { createAdapter, type Adapter } from "./adapter";
import { CATALOG } from "./catalog";
import type { OpenState } from "./opener";
import { PicksContext, type Picks } from "./picks";
import { Surfaces } from "./Surfaces";

// A row that opens in place: a click on it (not on its box) opens it and
// reads the item once, its disclosure button says so, a click again or ✕
// closes it; what was read is text, marked when its server is not
// trusted, with the item's note; a read that needs a yes asks first.

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const surface = "n1--mails";
const archive: PickAction = { name: "archive", one: "Déplacer 1 élément", many: "Déplacer {n} éléments", tool: "mail/move" };
const messages = [
  {
    version: "1.0" as const,
    createSurface: {
      surfaceId: surface,
      catalogId: CATALOG.id,
      components: [
        { id: "root", component: "List", children: { path: "/inbox/messages", componentId: "row" } },
        { id: "row", component: "Text", text: { "@path": "subject" } },
      ],
      dataModel: { inbox: { messages: [{ id: "m1", subject: "Facture" }, { id: "m2", subject: "Relance" }] } },
    },
  },
];

let el: HTMLDivElement;
let root: Root;
let adapter: Adapter;
let reads: { item: string; yes: boolean }[];
let answer: (item: string, yes: boolean) => Opened;

async function mount(actions: PickAction[] = []) {
  const list: PickList = {
    component: "root",
    source: "inbox",
    open: true,
    items: [
      { n: 0, id: "m1", title: "Facture" },
      { n: 1, id: "m2", title: "Relance" },
    ],
    actions,
  };
  const picks: Picks = {
    list: (s, c) => (s === surface && c === "root" ? list : undefined),
    kept: new Map(),
    run: async () => ({ ok: true, text: "" }),
    open: async (_s, _l, item, yes) => {
      reads.push({ item, yes });
      return answer(item, yes);
    },
    opened: new Map<string, OpenState>(),
  };
  adapter = createAdapter("fr");
  root = createRoot(el);
  await act(async () =>
    root.render(
      <WordsContext.Provider value={{ t: makeT(strings.fr), lang: "fr" }}>
        <PicksContext.Provider value={picks}>
          <Surfaces adapter={adapter} />
        </PicksContext.Provider>
      </WordsContext.Provider>,
    ),
  );
  await act(async () => adapter.applyMessages(messages));
}

const heads = () => [...el.querySelectorAll<HTMLElement>(".a2-openhead")];
const disclosures = () => [...el.querySelectorAll<HTMLButtonElement>(".a2-openbtn")];
const click = (e: Element | null | undefined) => act(async () => (e as HTMLElement).click());

beforeEach(() => {
  el = document.createElement("div");
  document.body.append(el);
  reads = [];
  answer = (item) => ({
    title: item === "m1" ? "Facture" : "Relance",
    from: "Sam",
    body: "<b>Le texte</b>",
    raw: '{"subject": "Facture"}',
    external: true,
    note: { group: "À supprimer", note: "Une promo" },
  });
});

afterEach(() => {
  act(() => root.unmount());
  adapter.dispose();
  el.remove();
});

describe("an item opened in place", () => {
  it("opens on a click, reads once, and closes on a click again or ✕", async () => {
    await mount();
    expect(disclosures()[0].getAttribute("aria-expanded")).toBe("false");
    expect(disclosures()[0].getAttribute("aria-label")).toBe("Ouvrir « Facture »");
    await click(heads()[0].querySelector(".a2-text"));
    const panel = el.querySelector(".a2-opened")!;
    expect(disclosures()[0].getAttribute("aria-expanded")).toBe("true");
    expect(disclosures()[0].getAttribute("aria-controls")).toBe(panel.id);
    expect(panel.querySelector(".a2-opentext")?.textContent).toBe("<b>Le texte</b>"); // text, never HTML
    expect(panel.querySelector("b")).toBeNull();
    expect(panel.querySelector(".a2-text-mark")?.textContent).toBe("extérieur");
    expect(panel.querySelector(".a2-opennote")?.textContent).toBe("Classé « À supprimer » : Une promo");
    expect(panel.querySelector(".a2-openraw summary")?.textContent).toBe("Détails");
    expect(reads).toEqual([{ item: "m1", yes: false }]);
    await click(heads()[0].querySelector(".a2-text"));
    expect(el.querySelector(".a2-opened")).toBeNull();
    await click(disclosures()[0]); // the keyboard's way: its button
    expect(el.querySelector(".a2-opened")).not.toBeNull();
    expect(reads).toHaveLength(1); // kept for the page's session
    await click(el.querySelector(".a2-openclose")!);
    expect(el.querySelector(".a2-opened")).toBeNull();
  });

  it("does not open when the row's box is clicked", async () => {
    await mount([archive]);
    const box = el.querySelectorAll<HTMLInputElement>(".a2-pickable .a2-pick")[1];
    await click(box);
    expect(box.checked).toBe(true);
    expect(el.querySelector(".a2-opened")).toBeNull();
    expect(reads).toEqual([]);
    await click(heads()[1].querySelector(".a2-text"));
    expect(reads).toEqual([{ item: "m2", yes: false }]);
  });

  it("asks a yes first for a read that needs one, then reads", async () => {
    answer = (_item, yes) => (yes ? { body: "Le texte" } : { ask: { tool: "mail/get", args: ["id : m1"] } });
    await mount();
    await click(disclosures()[0]);
    const ask = el.querySelector(".a2-openask")!;
    expect(ask.textContent).toContain("mail/get");
    expect(ask.textContent).toContain("id : m1");
    await click(ask.querySelector("button")!);
    expect(reads).toEqual([
      { item: "m1", yes: false },
      { item: "m1", yes: true },
    ]);
    expect(el.querySelector(".a2-opentext")?.textContent).toBe("Le texte");
  });

  it("stays open when the page draws its blocks again", async () => {
    await mount();
    await click(disclosures()[1]);
    await act(async () => adapter.applyMessages([{ version: "1.0", deleteSurface: { surfaceId: surface } }]));
    await act(async () => adapter.applyMessages(messages));
    expect(el.querySelector(".a2-opentitle")?.textContent).toBe("Relance");
    expect(reads).toHaveLength(1);
  });
});

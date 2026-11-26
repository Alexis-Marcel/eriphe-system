// @vitest-environment jsdom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { makeT, WordsContext } from "../i18n";
import strings from "../strings.json";
import { PageBody, type BodyProps } from "./Body";
import { dayOf, figure, plain } from "./line";
import { readPage, type MdBlock } from "./read";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

// A node's page drawn from its Markdown: what its file says is on the
// page, every fixture page of core/nodefile/testdata/pages, the faulty
// ones too; a tick, an ask, and the places cards and actions will take.
const pages = import.meta.glob<string>("../../../../nodefile/testdata/pages/*.md", { query: "?raw", import: "default", eager: true });
const page = (name: string) => pages[`../../../../nodefile/testdata/pages/${name}.md`];

let el: HTMLDivElement;
let root: Root;
beforeEach(() => {
  el = document.createElement("div");
  document.body.append(el);
  root = createRoot(el);
});
afterEach(() => {
  act(() => root.unmount());
  el.remove();
});

async function draw(props: BodyProps) {
  await act(async () =>
    root.render(
      <WordsContext.Provider value={{ t: makeT(strings.fr), lang: "fr" }}>
        <PageBody {...props} />
      </WordsContext.Provider>,
    ),
  );
}

// openAll opens every fold, detail and event of the page.
async function openAll() {
  for (let i = 0; i < 50; i++) {
    const shut = el.querySelector<HTMLElement>('[aria-expanded="false"]');
    if (!shut) return;
    await act(async () => shut.click());
  }
}

// words are a text's words a reader would look for: letters and numbers,
// three or more.
const words = (s: string) => (s.normalize("NFC").match(/[\p{L}\p{N}]{3,}/gu) ?? []).map((w) => w.toLowerCase());

// written is every piece of text the page's blocks hold, as lines; but
// what a callout's component draws in its own words: an agenda's days
// (« Samedi 28 novembre » is its third column, « sam. 28 »), a chart's x
// column's name (its axis), a person's « Rôle : » (the line under the name).
function written(bs: MdBlock[]): string[] {
  const out: string[] = [];
  for (const c of bs) {
    let b = c;
    if (b.type === "agenda") b = { ...b, body: b.body?.filter((x) => x.kind !== "heading") };
    if (b.type === "chart") b = { ...b, body: b.body?.map((x) => (x.kind === "table" ? { ...x, head: x.head!.slice(1) } : x)) };
    if (b.type === "person") b = { ...b, body: b.body?.map((x) => (x.items ? { ...x, items: x.items.map((it) => ({ ...it, text: it.text.replace(/^R[ôo]le\s*:/, "") })) } : x)) };
    if (b.text && b.kind !== "code") out.push(...b.text.split("\n"));
    if (b.kind === "code" && b.lang !== "eriphe:block") out.push(b.text ?? "");
    if (b.title) out.push(b.title);
    if (b.from) out.push(b.from);
    if (b.name) out.push(b.name);
    for (const h of b.head ?? []) out.push(h);
    for (const r of b.rows ?? []) out.push(...r);
    for (const it of b.items ?? []) {
      out.push(it.text);
      out.push(...written(it.detail ?? []));
    }
    out.push(...written(b.body ?? []));
  }
  return out;
}

describe("a page drawn from its Markdown", () => {
  for (const md of Object.keys(pages)) {
    it(`${md.split("/").pop()} shows all it says`, async () => {
      const body = pages[md];
      await draw({ body });
      await openAll();
      const p = readPage(body);
      const texts: string[] = [];
      const walk = document.createTreeWalker(el, NodeFilter.SHOW_TEXT);
      while (walk.nextNode()) texts.push(walk.currentNode.textContent ?? "");
      const shown = [...texts, ...[...el.querySelectorAll("[title]")].map((x) => x.getAttribute("title")), ...[...el.querySelectorAll("[aria-label]")].map((x) => x.getAttribute("aria-label"))].join(" ");
      const have = new Set(words(shown));
      const lost = written(p.blocks).flatMap((l) => words(plain(l))).filter((w) => !have.has(w));
      expect(lost, "words written but not shown").toEqual([]);
      if (p.written) expect(el.querySelector(".md-written")?.textContent).toBe(p.written.text);
    });
  }

  it("draws each callout with its component", async () => {
    await draw({ body: page("projet") });
    expect(el.querySelector(".a2-timeline")).not.toBeNull();
    expect(el.querySelectorAll(".a2-step")).toHaveLength(4);
    expect(el.querySelector(".a2-step-current")?.textContent).toContain("Corriger et tester");
    expect(el.querySelector(".md-aside .a2-figs")).not.toBeNull(); // [!figures|aside]
    expect(el.querySelector(".a2-code")).not.toBeNull();
    await draw({ body: page("terrasse") });
    expect(el.querySelector(".md-aside .a2-person-name")?.textContent).toBe("Sam");
    expect(el.querySelector(".a2-person-facts a[href='tel:0123456789']")).not.toBeNull();
    expect(el.querySelector(".a2-quote .a2-quote-when")?.getAttribute("dateTime")).toBe("2026-11-24"); // « 24/11 », the year the page's
    expect(el.querySelector(".a2-chart, svg")).not.toBeNull();
    await draw({ body: page("journee") });
    expect(el.querySelector(".a2-agenda")).not.toBeNull();
    expect([...el.querySelectorAll(".a2-agenda .a2-ev-place")].map((x) => x.textContent)).toContain("parc"); // the second day's place too
    await draw({ body: page("portefeuille") });
    expect(el.querySelector(".a2-alert-warning .a2-alert-title")?.textContent).toBe("Obligations sous la cible");
    expect(el.querySelectorAll(".md-table tbody tr")).toHaveLength(5);
    expect(el.querySelector(".md-table td.a2-col-number")).not.toBeNull();
  });

  it("keeps bold and links in a table, a quote, an alert and a person", async () => {
    await draw({
      body: "| Qui | Où |\n|---|---|\n| **Sam** | [site](https://example.org) |\n\n> Il a dit **oui**.\n> — Sam, 2026-11-24\n\n> [!info] **Note**\n> Voir [[Mes mails]].\n\n> [!person] Sam\n> - Rôle : **artisan**\n> - Site : [ici](https://example.org/sam)\n",
      wiki: { find: (name) => ({ id: "n2", title: name, named: true }), open: () => {}, none: "", archived: (x) => x },
    });
    expect(el.querySelector(".md-table strong")?.textContent).toBe("Sam");
    expect(el.querySelector(".md-table a")?.getAttribute("href")).toBe("https://example.org/");
    expect(el.querySelector(".a2-quote-text strong")?.textContent).toBe("oui");
    expect(el.querySelector(".a2-alert-title strong")?.textContent).toBe("Note");
    expect(el.querySelector(".a2-alert-text .a2-wiki")?.textContent).toBe("Mes mails");
    expect(el.querySelector(".a2-person-detail strong")?.textContent).toBe("artisan");
    expect(el.querySelector(".a2-person-facts a")?.getAttribute("href")).toBe("https://example.org/sam");
  });

  it("opens an item's detail on a click, and an event's", async () => {
    await draw({ body: page("mails") });
    const row = el.querySelector<HTMLElement>('.md-row[aria-expanded="false"]')!;
    expect(el.querySelector(".md-detail")).toBeNull();
    await act(async () => row.click());
    expect(el.querySelector(".md-detail")?.textContent).toContain("rubrique Mes voyages");
    await draw({ body: page("journee") });
    const ev = [...el.querySelectorAll<HTMLElement>(".a2-ev-open")].find((x) => x.textContent === "Revue du projet Terrasse")!;
    await act(async () => ev.click());
    expect(el.querySelector(".a2-agenda-more")?.textContent).toContain("Invitation de Sam");
    expect(el.querySelector(".a2-agenda-more .md-ref")?.getAttribute("title")).toBe("event:ev-invit");
  });

  it("ticks a task: its line and state to eriphe-core, the box at once, back when it did not go", async () => {
    let ok = true;
    const tick = vi.fn(async () => ok);
    await draw({ body: page("taches"), tick });
    const boxes = el.querySelectorAll<HTMLInputElement>("input.a2-tick");
    expect(boxes).toHaveLength(6);
    await act(async () => boxes[0].click());
    expect(tick).toHaveBeenCalledWith(expect.objectContaining({ line: 3 }), "- [ ] Valider l'option de train : référence ABC123 📅 2026-11-27", "x");
    expect(boxes[0].checked).toBe(true);
    await act(async () => boxes[4].click()); // done: unticked
    expect(tick).toHaveBeenLastCalledWith(expect.objectContaining({ line: 7 }), "- [x] Payer la cantine 📅 2026-11-25", " ");
    ok = false;
    await act(async () => boxes[1].click());
    expect(boxes[1].checked).toBe(false);
    expect(boxes[5].disabled).toBe(true); // dropped
    expect(boxes[2].indeterminate).toBe(true); // in progress
  });

  it("ticks nothing without tick (an old version)", async () => {
    await draw({ body: page("taches") });
    expect([...el.querySelectorAll<HTMLInputElement>("input.a2-tick")].every((b) => b.disabled)).toBe(true);
  });

  it("asks with an ask: link, by its line and place", async () => {
    const ask = vi.fn();
    await draw({ body: page("portefeuille"), ask });
    const buttons = el.querySelectorAll<HTMLButtonElement>(".a2-asks .a2-ask");
    expect([...buttons].map((b) => b.textContent)).toEqual(["Propose un rééquilibrage", "Montre les frais de l'année"]);
    expect(buttons[0].classList.contains("a2-button-primary")).toBe(true);
    await act(async () => buttons[1].click());
    const line = page("portefeuille").split("\n").findIndex((l) => l.includes("(ask:)")) + 1;
    expect(ask).toHaveBeenCalledWith(line, 1);
  });

  it("leaves a place for a line's cards and a heading's action", async () => {
    const item = vi.fn(() => <i className="slot-card" />);
    const link = vi.fn(() => <i className="slot-act" />);
    await draw({ body: page("mails"), slots: { item, link } });
    expect(el.querySelectorAll(".md-row .slot-card")).toHaveLength(6);
    expect(item).toHaveBeenCalledWith(expect.objectContaining({ line: 4, task: " " }));
    // do: under its heading, card: in an item's detail: as eriphe-core reads them.
    expect(link).toHaveBeenCalledWith({ kind: "do", label: "Archiver ces mails", target: "mail/move", line: 12, alone: true, heading: 11 });
    expect(el.querySelectorAll(".md-acts .slot-act")).toHaveLength(2);
    await act(async () => [...el.querySelectorAll<HTMLElement>('.md-row[aria-expanded="false"]')][1].click());
    expect(link).toHaveBeenCalledWith(expect.objectContaining({ kind: "card", target: "calls:412", item: 9, line: 9, heading: 3 }));
    // Without slots, their words, quiet.
    await draw({ body: page("mails") });
    expect([...el.querySelectorAll(".md-link-do")].map((x) => x.textContent)).toEqual(["Archiver ces mails", "Jeter ces mails"]);
  });

  it("draws a legacy block where its fence is", async () => {
    const legacy = vi.fn((name: string) => <section className="legacy">{name}</section>);
    await draw({ body: "Avant.\n\n```eriphe:block vue\n{}\n```\n\nAprès.\n", legacy });
    expect(legacy).toHaveBeenCalledWith("vue");
    expect([...el.querySelectorAll(".md-p, .legacy")].map((x) => x.textContent)).toEqual(["Avant.", "vue", "Après."]);
  });
});

describe("a page's lines", () => {
  it("reads a figure with two units", () => {
    const f = figure("PEA : 18 385 € (▲ 1,8 % cette semaine)");
    expect([plain(f.label), f.amount, f.delta, f.deltaUnit, f.deltaLabel]).toEqual(["PEA", { n: 18385, unit: "€" }, 1.8, "%", "cette semaine"]);
    expect(figure("Valeur : 18 420 € (▼ 1,1 % aujourd'hui)").delta).toBe(-1.1);
    expect(figure("Taille : 38,2 Mo (▲ 0,1 Mo)")).toMatchObject({ amount: { n: 38.2, unit: "Mo" }, delta: 0.1, deltaUnit: "Mo" });
    expect(figure("Tests : 44/44").amount).toBeUndefined();
    expect(figure("Écart : 3 (▲ 2 points de plus)")).toMatchObject({ delta: 2, deltaLabel: "points de plus" });
    expect(figure("PEA : [18 385 €](on:monitor.changed/pea)").amount).toEqual({ n: 18385, unit: "€" });
  });

  it("reads a day in ISO, or with its year, never guessing one", () => {
    expect(dayOf("Jeudi 2026-11-26")).toBe("2026-11-26");
    expect(dayOf("26 novembre 2026")).toBe("2026-11-26");
    expect(dayOf("26/11/2026")).toBe("2026-11-26");
    expect(dayOf("Jeudi 26 novembre")).toBeUndefined();
    expect(dayOf("Jeudi 26 novembre", 2026)).toBe("2026-11-26");
    expect(dayOf("24/11", 2026)).toBe("2026-11-24");
    expect(dayOf("2026-02-30")).toBeUndefined();
  });
});

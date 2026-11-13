// @vitest-environment jsdom
import { act, StrictMode } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { makeT, WordsContext, type Lang } from "../i18n";
import strings from "../strings.json";
import { createAdapter, type ActionMessage, type Adapter, type ErrorMessage, type Message } from "./adapter";
import { CATALOG } from "./catalog";
import { galleryFixtures, sampleProposals } from "./fixtures";
import { safeHref } from "./markdown";
import { ProposalsContext, type Answer, type Moved, type Proposals } from "./proposals";
import { Surfaces } from "./Surfaces";

// Every component of the catalog, rendered from the gallery's data
// (gallery.json, which eriphe-core accepts) in both languages: what it
// shows, what it does, and the basics of access: roles, names, focus, and
// no style attribute.

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

let el: HTMLDivElement;
let root: Root;
let adapter: Adapter;
let errors: ErrorMessage[];
let actions: ActionMessage[];
let answers: Answer[];

// The page's words in a language, as /espace/boot gives them.
const words = (lang: Lang) => ({ t: makeT(strings[lang]), lang });

// No proposal moves and no day has ghosts, unless a test says so.
const quiet = { day: async () => [], follow: () => () => {} };

async function mount(lang: Lang, msgs: Message[] = galleryFixtures(lang), proposals?: Partial<Proposals>) {
  const sample = sampleProposals(lang);
  const src: Proposals = {
    ...quiet,
    ...(proposals ?? {
      read: sample.read,
      answer: async (a: Answer) => {
        answers.push(a);
        return sample.answer(a);
      },
    }),
  } as Proposals;
  adapter = createAdapter(lang);
  adapter.onError((e) => errors.push(e));
  adapter.onAction((a) => actions.push(a));
  root = createRoot(el);
  await act(async () =>
    root.render(
      <StrictMode>
        <WordsContext.Provider value={words(lang)}>
          <ProposalsContext.Provider value={src}>
            <Surfaces adapter={adapter} />
          </ProposalsContext.Provider>
        </WordsContext.Provider>
      </StrictMode>,
    ),
  );
  await act(async () => adapter.applyMessages(msgs));
}

beforeEach(() => {
  // Now is the gallery's morning: 10:00 UTC on 12 November 2026.
  vi.useFakeTimers({ toFake: ["Date"] });
  vi.setSystemTime(new Date("2026-11-12T10:00:00Z"));
  el = document.createElement("div");
  document.body.append(el);
  errors = [];
  actions = [];
  answers = [];
});

afterEach(async () => {
  await act(async () => root.unmount());
  adapter.dispose();
  el.remove();
  vi.useRealTimers();
});

const $ = (sel: string) => el.querySelector(sel) as HTMLElement;
const $$ = (sel: string) => [...el.querySelectorAll(sel)] as HTMLElement[];
const text = (sel: string) => $(sel)?.textContent ?? null;
const surface = (id: string) => adapter.surfaces().find((s) => s.id === id)!;
const click = (n: Element) => act(async () => (n as HTMLElement).click());

// type sets an input's value as the user does, so React sees it.
async function type(input: HTMLInputElement | HTMLTextAreaElement, value: string) {
  const proto = input instanceof HTMLTextAreaElement ? HTMLTextAreaElement.prototype : HTMLInputElement.prototype;
  await act(async () => {
    Object.getOwnPropertyDescriptor(proto, "value")!.set!.call(input, value);
    input.dispatchEvent(new Event("input", { bubbles: true }));
  });
}

const one = (components: unknown[], dataModel: unknown = {}): Message[] => [
  { version: "1.0", createSurface: { surfaceId: "t", catalogId: CATALOG.id, components, dataModel } },
];

// What each component of the catalog renders as, to find it.
const MARK: Record<string, string> = {
  Text: ".a2-text", Markdown: ".a2-markdown", Image: ".a2-image-off", Icon: ".a2-row > .a2-icon", Link: "a.a2-link", Code: ".a2-code-diff .a2-diff-add", Row: ".a2-row",
  Column: ".a2-column", List: ".a2-list", Card: ".a2-card", Tabs: ".a2-tabs", Divider: "hr.a2-divider",
  Button: "button.a2-button", Choice: "fieldset.a2-choice", TextField: ".a2-field input[type=text]",
  DateField: ".a2-field input[type=datetime-local]", Metric: ".a2-metric", Chart: ".a2-chart", Table: "table.a2-table",
  Timeline: "ol.a2-timeline", Message: "article.a2-message", Day: ".a2-day", Proposal: ".a2-proposal-pending",
  Alert: ".a2-alert", Placeholder: ".a2-placeholder",
};

// The accessible name of an element, as far as these components give one.
function named(n: HTMLElement): boolean {
  const by = n.getAttribute("aria-labelledby");
  if (n.getAttribute("aria-label")?.trim()) return true;
  if (by && by.split(" ").every((id) => document.getElementById(id)?.textContent?.trim())) return true;
  if (n.id && document.querySelector(`label[for="${CSS.escape(n.id)}"]`)) return true;
  if (n.closest("label")?.textContent?.trim()) return true;
  return !!n.textContent?.trim();
}

for (const lang of ["fr", "en"] as const) {
  describe(`the catalog's components (${lang})`, () => {
    it("render the gallery, every one, with classes only", async () => {
      await mount(lang);
      await act(async () => {}); // the proposal's read
      expect(errors).toEqual([]);
      for (const [name, sel] of Object.entries(MARK)) expect($(sel), name).not.toBeNull();
      expect(document.querySelectorAll("[style]")).toHaveLength(0);
      expect(document.querySelectorAll("style, script, iframe, object")).toHaveLength(0);
      expect($$("*").filter((n) => n.tagName.includes("-"))).toEqual([]);
    });

    it("give access: names, roles, focus", async () => {
      await mount(lang);
      await act(async () => {});
      for (const b of $$("button")) expect(named(b), b.outerHTML).toBe(true);
      for (const i of $$("input, textarea")) expect(named(i), i.outerHTML).toBe(true);
      for (const n of $$("[role=img]")) expect(n.getAttribute("aria-label"), n.outerHTML).toBeTruthy();
      for (const n of $$("img")) expect(n.hasAttribute("alt")).toBe(true);
      for (const s of $$("svg.a2-icon")) expect(s.getAttribute("aria-hidden")).toBe("true");
      for (const n of $$("[tabindex]")) expect(Number(n.getAttribute("tabindex"))).toBeLessThanOrEqual(0);
      for (const a of $$("a[href]")) expect(a.getAttribute("rel")).toContain("noopener");
      expect($("[role=tablist] [role=tab][aria-selected=true]")).not.toBeNull();
      expect($$("[role=tabpanel]").filter((p) => !p.hidden)).toHaveLength(1);
      expect($(".a2-chart svg[role=img]").getAttribute("aria-label")).toBeTruthy();
      expect($("[aria-current=step]")?.textContent).toContain(lang === "fr" ? "Intégration" : "Build");
      expect($(".a2-alert-warning").getAttribute("role")).toBe("alert");
      expect($(".a2-alert-info").getAttribute("role")).toBe("status");
      expect($(".a2-placeholder").getAttribute("role")).toBe("status");
    });

    it("write their fixed words and numbers in the language", async () => {
      await mount(lang);
      await act(async () => {});
      const fr = lang === "fr";
      expect(text(".a2-proposal-actions")).toBe(fr ? "OuiNon" : "YesNo");
      expect(text(".a2-proposal-kicker")).toBe(fr ? "Proposition" : "Proposal");
      expect(text(".a2-more")).toBe(fr ? "Lire tout" : "Read all");
      expect(text(".a2-step-done .sr-only")).toBe(fr ? ", terminé" : ", done");
      expect(text(".a2-day-label")).toBe(fr ? "jeudi 12 novembre" : "Thursday 12 November");
      expect($$(".a2-metric-value").map((n) => n.textContent)).toEqual(fr ? ["37 %", "1,2 To", "74,5 kWh"] : ["37 %", "1.2 TB", "74.5 kWh"]);
      expect($$(".a2-table tbody tr:first-child td").map((n) => n.textContent)).toEqual(
        fr ? ["Formulaire de contact", "4", "14 nov."] : ["Contact form", "4", "14 Nov"],
      );
      // The functions: formatString, formatDate, formatRelative.
      const captions = $$(".a2-text-caption").map((n) => n.textContent);
      expect(captions).toContain(fr ? "3 trajets comparés" : "3 routes compared");
      expect(captions).toContain(fr ? "jeudi 12 novembre" : "Thursday 12 November");
      expect(captions).toContain(fr ? "il y a 3 heures" : "3 hours ago");
      // A message of today says its time, an older one its date.
      expect($$(".a2-message-date").map((n) => n.textContent)).toEqual(fr ? ["08 h 10", "11 nov., 17 h 40"] : ["08:10", "11 Nov, 17:40"]);
      expect(text(".a2-image-off")).toBe(fr ? "Plan du quartier" : "Map of the area");
    });

    it("say in preparation, and nothing to show, in the language", async () => {
      await mount(lang, one([
        { id: "root", component: "Column", children: ["p", "tb", "ch", "d"] },
        { id: "p", component: "Placeholder" },
        { id: "tb", component: "Table", columns: [{ key: "a", label: "A" }], rows: [] },
        { id: "ch", component: "Chart", kind: "line", series: { "@path": "/none" } },
        { id: "d", component: "Day", date: "2026-11-12", events: [] },
      ]));
      const fr = lang === "fr";
      expect(text(".a2-placeholder-text")).toBe(fr ? "En préparation" : "In preparation");
      expect($$(".a2-empty").map((n) => n.textContent)).toEqual(
        fr ? ["Aucune ligne", "Pas encore de données", "Rien de prévu"] : ["No rows", "No data yet", "Nothing planned"],
      );
      expect(errors).toEqual([]);
    });
  });
}

describe("the catalog's components", () => {
  it("move between tabs with the keyboard", async () => {
    await mount("fr");
    const tabs = $$("[role=tab]");
    tabs[0].focus();
    await act(async () => tabs[0].dispatchEvent(new KeyboardEvent("keydown", { key: "ArrowRight", bubbles: true })));
    expect(document.activeElement).toBe(tabs[1]);
    expect(tabs[1].getAttribute("aria-selected")).toBe("true");
    expect(tabs[0].tabIndex).toBe(-1);
    const shown = $$("[role=tabpanel]").find((p) => !p.hidden)!;
    expect(shown.getAttribute("aria-labelledby")).toBe(tabs[1].id);
    expect(shown.querySelector("table")).not.toBeNull();
    await act(async () => tabs[1].dispatchEvent(new KeyboardEvent("keydown", { key: "End", bubbles: true })));
    expect(document.activeElement).toBe(tabs[2]);
    await act(async () => tabs[2].dispatchEvent(new KeyboardEvent("keydown", { key: "ArrowRight", bubbles: true })));
    expect(document.activeElement).toBe(tabs[0]);
  });

  it("write what the user enters to the surface's data, and follow it", async () => {
    await mount("fr");
    const trip = surface("gallery-trip");
    await click($$(".a2-option input").find((i) => (i as HTMLInputElement).value === "car")!);
    expect(trip.dataModel.get("/how")).toEqual(["car"]);
    expect(($$(".a2-option input").find((i) => (i as HTMLInputElement).value === "train") as HTMLInputElement).checked).toBe(false);
    await type($(".a2-field input[type=text]") as HTMLInputElement, "Partir tôt");
    expect(trip.dataModel.get("/note")).toBe("Partir tôt");
    await type($("input[type=datetime-local]") as HTMLInputElement, "2026-11-21T08:00");
    expect(trip.dataModel.get("/leave")).toBe("2026-11-21T08:00");
    // The data changes: the fields show it.
    await act(async () => adapter.applyMessages([{ version: "1.0", updateDataModel: { surfaceId: "gallery-trip", path: "/note", value: "Autre" } }]));
    expect(($(".a2-field input[type=text]") as HTMLInputElement).value).toBe("Autre");
    // The ask carries its author's words only, never the fields.
    await click($$("button.a2-button").find((b) => b.textContent?.includes("itinéraire"))!);
    expect(actions.at(-1)?.action).toMatchObject({ name: "ask", sourceComponentId: "ask", surfaceId: "gallery-trip" });
    expect(actions.at(-1)?.action.context).toEqual({ text: "Trouve-moi un itinéraire pour le voyage" });
  });

  it("let a choice take several values when asked", async () => {
    await mount("en", one([
      { id: "root", component: "Choice", multiple: true, options: [{ label: "A", value: "a" }, { label: "B", value: "b" }], value: { "@path": "/v" } },
    ], { v: ["a"] }));
    expect($$("input[type=checkbox]")).toHaveLength(2);
    await click($$("input")[1]);
    expect(surface("t").dataModel.get("/v")).toEqual(["a", "b"]);
    await click($$("input")[0]);
    expect(surface("t").dataModel.get("/v")).toEqual(["b"]);
  });

  it("open a message in full in place", async () => {
    await mount("en");
    const more = $(".a2-more");
    expect(more.getAttribute("aria-expanded")).toBe("false");
    expect($(".a2-message-body")).toBeNull();
    await click(more);
    expect(more.getAttribute("aria-expanded")).toBe("true");
    expect(text(".a2-message-body")).toContain("The lead time is still three weeks.");
    expect(document.getElementById(more.getAttribute("aria-controls")!)).toBe($(".a2-message-body"));
    // A sender is its name; a message without a body has no button.
    expect($$(".a2-message-from").map((n) => n.textContent)).toEqual(["Sam", "The school"]);
    expect($$(".a2-more")).toHaveLength(1);
  });

  it("place a day's events by the hour, overlaps side by side", async () => {
    await mount("fr");
    const evs = $$(".a2-day-grid .a2-ev");
    expect(evs.map((e) => e.querySelector(".a2-ev-title")?.textContent)).toEqual(["Point d'équipe", "Atelier devis", "Appel avec Sam", "Dentiste"]);
    // From 9:00: half-hour rows.
    expect(evs[0].className).toMatch(/a2-r-0 a2-span-1 a2-lane-all/);
    expect(evs[1].className).toMatch(/a2-r-2 a2-span-3 a2-lane-0 a2-ev-overlap/);
    expect(evs[2].className).toMatch(/a2-r-4 a2-span-2 a2-lane-1 a2-ev-overlap/);
    expect(evs[3].className).toMatch(/a2-r-10 a2-span-2 a2-lane-all/);
    expect(evs[3].className).not.toMatch(/overlap/);
    expect($(".a2-day-grid").className).toContain("a2-lanes-2");
    expect(evs[1].querySelector(".sr-only")?.textContent).toBe("chevauche un autre événement");
    expect(text(".a2-ev-time")).toBe("09 h 00–09 h 30");
    expect(text(".a2-day-grid .a2-ev-title")).toBe("Point d'équipe");
    expect(text(".a2-day-allday")).toBe("Anniversaire de Sam");
    expect($$(".a2-hour").map((h) => h.textContent)).toEqual(["09", "10", "11", "12", "13", "14"]);
  });

  it("show only the day's own events", async () => {
    await mount("en", one([
      {
        id: "root",
        component: "Day",
        date: "2026-11-12",
        events: [
          { title: "Today", start: "2026-11-12", allDay: true },
          { title: "Tomorrow", start: "2026-11-13", allDay: true },
          { title: "Yesterday at 9", start: "2026-11-11T09:00:00", end: "2026-11-11T10:00:00" },
          { title: "Night", start: "2026-11-11T22:00:00", end: "2026-11-12T01:00:00" },
          { title: "Trip", start: "2026-11-10", end: "2026-11-14", allDay: true },
        ],
      },
    ]));
    expect($$(".a2-day-allday .a2-ev-title").map((n) => n.textContent)).toEqual(["Today", "Trip"]);
    expect($$(".a2-day-grid .a2-ev-title").map((n) => n.textContent)).toEqual(["Night"]);
    expect($(".a2-day-grid .a2-ev").className).toMatch(/a2-r-0 a2-span-2/);
  });

  it("colour a change by whether it is good news", async () => {
    await mount("fr");
    const deltas = $$(".a2-delta");
    expect(deltas.map((d) => d.className.match(/a2-delta-(\w+)/)?.[1])).toEqual(["good", "bad", "bad"]);
    expect(deltas[0].querySelector(".sr-only")?.textContent).toBe("en baisse de 5 %");
    expect(deltas[2].querySelector("[aria-hidden=true]:not(svg)")?.textContent).toBe("+3,4 kWh");
  });

  it("draw charts in SVG, with their numbers for screen readers", async () => {
    await mount("en");
    const [bars, lines] = $$(".a2-chart");
    expect(bars.querySelectorAll("path.a2-bar")).toHaveLength(7);
    expect(bars.querySelector("path.a2-bar title")?.textContent).toBe("Home, Mon: 12.5 kWh");
    expect(lines.querySelectorAll("polyline.a2-line")).toHaveLength(2);
    expect(lines.querySelector(".a2-s1 polyline")).not.toBeNull();
    expect([...lines.querySelectorAll(".a2-legend-item")].map((n) => n.textContent)).toEqual(["Inside", "Outside"]);
    expect(lines.querySelector("svg")?.getAttribute("aria-label")).toBe("Lines: Inside, Outside");
    expect(lines.querySelectorAll("table.sr-only tbody tr")).toHaveLength(6);
    // Times of one day are told by the hour.
    expect([...lines.querySelectorAll(".a2-grid text")].map((n) => n.textContent).slice(-6)).toEqual(["06:00", "09:00", "12:00", "15:00", "18:00", "21:00"]);
    // A bound series follows the data.
    await act(async () =>
      adapter.applyMessages([{ version: "1.0", updateDataModel: { surfaceId: "gallery-figures", path: "/temps", value: [{ name: "One", points: [{ x: 1, y: 2 }, { x: 2, y: 3 }] }] } }]),
    );
    expect($$(".a2-chart")[1].querySelectorAll("polyline")).toHaveLength(1);
  });

  it("show a step's state and its date", async () => {
    await mount("fr");
    expect($$("[data-surface=gallery-project] .a2-step").map((s) => s.className.replace("a2-step a2-step-", ""))).toEqual(["done", "done", "current", "pending"]);
    expect(text(".a2-step-done .a2-step-date")).toBe(" 3 nov.");
    expect(text(".a2-step-current .a2-step-detail")).toBe("Pages d'accueil et d'agenda");
  });

  it("answer the one proposal it shows, and say how it was decided", async () => {
    await mount("fr");
    await act(async () => {});
    expect(text(".a2-proposal-text")).toBe("Ne plus te prévenir des factures avant le brief ?");
    expect(text(".a2-proposal-why")).toBe("Tu n'as ouvert aucune des cinq dernières.");
    await click($$(".a2-proposal-actions button")[0]);
    expect(answers).toEqual([{ surfaceId: "gallery-more", componentId: "proposal", ref: "rule:7", yes: true }]);
    expect($(".a2-proposal-actions")).toBeNull();
    expect(text(".a2-proposal-kicker")).toBe("Validé");
  });

  it("show a day's dated proposals as ghosts, and what they overlap", async () => {
    const src = sampleProposals("fr");
    await mount("fr", galleryFixtures("fr"), src);
    await act(async () => {});
    const ghost = $(".a2-day-grid .a2-ev-ghost");
    expect(ghost.getAttribute("data-proposal")).toBe("intention:3");
    expect(ghost.querySelector(".a2-ev-kicker")?.textContent).toBe("Proposé");
    expect(ghost.querySelector(".a2-ev-title")?.textContent).toBe("Une heure pour relire le devis");
    expect(ghost.querySelector(".a2-ev-time")?.textContent).toBe("14 h 30–15 h 30");
    // It overlaps the dentist (14:00–15:00): both say so, side by side.
    expect(ghost.className).toMatch(/a2-r-11 a2-span-2 a2-lane-1 a2-ev-ghost a2-ev-clash/);
    expect(ghost.querySelector(".a2-ev-clash-what")?.textContent).toBe("chevauche « Dentiste »");
    const dentist = $$(".a2-day-grid .a2-ev").find((e) => e.querySelector(".a2-ev-title")?.textContent === "Dentiste")!;
    expect(dentist.className).toMatch(/a2-lane-0 a2-ev-overlap a2-ev-clashed/);
    // An event is never a ghost; the hours reach the ghost's end.
    expect($$(".a2-ev-ghost")).toHaveLength(1);
    expect($$(".a2-hour").map((h) => h.textContent).at(-1)).toBe("15");

    // Answered anywhere, it leaves the day, and the dentist is alone again.
    await act(async () => void (await src.answer({ surfaceId: "", componentId: "", ref: "intention:3", yes: false })));
    await act(async () => {});
    expect($$(".a2-ev-ghost")).toHaveLength(0);
    const after = $$(".a2-day-grid .a2-ev").find((e) => e.querySelector(".a2-ev-title")?.textContent === "Dentiste")!;
    expect(after.className).not.toMatch(/clash|overlap/);
  });

  it("show no ghost on a day that is not the user's", async () => {
    let asked = 0;
    await mount("en", one([{ id: "root", component: "Day", date: "2026-11-12", events: [], proposals: false }]), {
      ...sampleProposals("en"),
      day: async () => (asked++, []),
    });
    await act(async () => {});
    expect(asked).toBe(0);
    expect(text(".a2-empty")).toBe("Nothing planned");
  });

  it("say a ghost drawn from external content is one, all day above", async () => {
    const view = { ref: "intention:5", card: 5, state: "pending", text: "Pay the supplier", tainted: true, when: [{ start: "2026-11-12", allDay: true }] };
    await mount("en", one([{ id: "root", component: "Day", date: "2026-11-12", events: [] }]), {
      read: async () => view,
      answer: async () => false,
      day: async (date) => (date === "2026-11-12" ? [view] : []),
    });
    await act(async () => {});
    const ghost = $(".a2-day-allday .a2-ev-ghost");
    expect(ghost.querySelector(".a2-ev-kicker")?.textContent).toBe("Proposed · external content");
    expect(ghost.querySelector(".a2-ev-title")?.textContent).toBe("Pay the supplier");
    expect($(".a2-empty")).toBeNull();
  });

  it("update a proposal answered elsewhere, and only on its own move", async () => {
    let state = "pending";
    let reads = 0;
    const followers = new Set<(m: Moved) => void>();
    const move = (m: Moved) => act(async () => followers.forEach((f) => f(m)));
    await mount("fr", one([{ id: "root", component: "Proposal", proposalId: "rule:9" }]), {
      read: async () => (reads++, { ref: "rule:9", card: 9, state, text: "Ranger les mails ?" }),
      answer: async () => false,
      follow: (on) => (followers.add(on), () => followers.delete(on)),
    });
    await act(async () => {});
    expect($(".a2-proposal-actions")).not.toBeNull();
    const before = reads;
    await move({ refs: ["rule:10"] });
    expect(reads).toBe(before);
    // Decided in the conversation: the stream says so, the surface reads it again.
    state = "approved";
    await move({ refs: ["rule:9"] });
    await act(async () => {});
    expect($(".a2-proposal-actions")).toBeNull();
    expect(text(".a2-proposal-kicker")).toBe("Validé");
    // A stream that fell behind: everything is read again.
    state = "rejected";
    await move({ refs: [], all: true });
    await act(async () => {});
    expect(text(".a2-proposal-kicker")).toBe("Refusé");
  });

  it("follow an approved call: running, then done or failed with our reason", async () => {
    let view: Record<string, unknown> = { ref: "access:4", card: 4, state: "approved", exec: "running", text: "Fusionner ?" };
    const followers = new Set<(m: Moved) => void>();
    const move = (m: Moved) => act(async () => followers.forEach((f) => f(m)));
    await mount("en", one([{ id: "root", component: "Proposal", proposalId: "access:4" }]), {
      read: async () => view as never,
      answer: async () => false,
      follow: (on) => (followers.add(on), () => followers.delete(on)),
    });
    await act(async () => {});
    expect(text(".a2-proposal-kicker")).toBe("Approved · running…");
    expect($(".a2-proposal-exec-running")?.getAttribute("aria-busy")).toBe("true");
    view = { ...view, exec: "done" };
    await move({ refs: ["access:4"] });
    await act(async () => {});
    expect(text(".a2-proposal-kicker")).toBe("Approved · done");
    view = { ...view, exec: "failed", execWhy: "error" };
    await move({ refs: ["access:4"] });
    await act(async () => {});
    expect(text(".a2-proposal-kicker")).toBe("Approved · failed");
    expect(text(".a2-proposal-failed")).toBe("The tool answered with an error.");
  });

  it("say when a proposal is gone, or an answer did not leave", async () => {
    const view = { ref: "rule:9", card: 9, state: "pending", text: "Ranger les mails ?" };
    await mount("fr", one([
      { id: "root", component: "Column", children: ["a", "b"] },
      { id: "a", component: "Proposal", proposalId: "rule:404" },
      { id: "b", component: "Proposal", proposalId: { "@path": "/p" } },
    ], { p: "rule:9" }), { read: async (ref) => (ref === "rule:9" ? view : null), answer: async () => false });
    await act(async () => {});
    expect(text(".a2-empty")).toBe("Cette proposition n'existe plus.");
    await click($$(".a2-proposal-actions button")[1]);
    expect(text(".a2-proposal-failed")).toBe("La réponse n'est pas partie : réessaie.");
    expect($(".a2-proposal-actions")).not.toBeNull();
  });

  it("leave a proposal on a surface showing external content to the conversation", async () => {
    const view = { ref: "rule:9", card: 9, state: "pending", text: "Ranger les mails ?" };
    const sent: Answer[] = [];
    const comps = [{ id: "root", component: "Proposal", proposalId: "rule:9" }];
    const metadata = { extensions: { eriphe: { title: "Mails", tainted: true } } };
    await mount("fr", [{ version: "1.0", createSurface: { surfaceId: "t", catalogId: CATALOG.id, components: comps, dataModel: {}, metadata } }], {
      read: async () => view,
      answer: async (a) => (sent.push(a), true),
    });
    await act(async () => {});
    expect(text(".a2-proposal-text")).toBe("Ranger les mails ?");
    expect($(".a2-proposal-actions")).toBeNull();
    expect(text(".a2-proposal-inchat")).toBe("Cette surface montre un contenu extérieur : réponds-y dans la conversation.");
    expect(sent).toEqual([]);
    // Code's own surface (the attention queue's): Oui and Non, as the card.
    await act(async () => adapter.applyMessages([{ version: "1.0", deleteSurface: { surfaceId: "t" } }]));
    const core = { extensions: { eriphe: { ...metadata.extensions.eriphe, author: "core" } } };
    await act(async () => adapter.applyMessages([{ version: "1.0", createSurface: { surfaceId: "t", catalogId: CATALOG.id, components: comps, dataModel: {}, metadata: core } }]));
    await act(async () => {});
    expect($(".a2-proposal-actions")).not.toBeNull();
  });

  it("say what a card asks by its kind: keep a memory or a reminder, or do something", async () => {
    const views: Record<string, { ref: string; card: number; kind: string; state: string; text: string }> = {
      "memory:1": { ref: "memory:1", card: 1, kind: "memory", state: "pending", text: "Sam prefers mornings" },
      "intention:2": { ref: "intention:2", card: 2, kind: "intention", state: "pending", text: "Add the event to the calendar" },
      "access:3": { ref: "access:3", card: 3, kind: "access", state: "pending", text: "Send the quote" },
    };
    for (const lang of ["fr", "en"] as const) {
      const fr = lang === "fr";
      for (const [ref, kicker, buttons] of [
        ["memory:1", fr ? "Garder en mémoire ?" : "Keep in memory?", fr ? "GarderOublier" : "KeepForget"],
        ["intention:2", fr ? "Garder ce rappel ?" : "Keep this reminder?", fr ? "GarderOublier" : "KeepForget"],
        ["access:3", fr ? "Proposition" : "Proposal", fr ? "OuiNon" : "YesNo"],
      ]) {
        await mount(lang, one([{ id: "root", component: "Proposal", proposalId: ref }]), {
          read: async (r) => views[r] ?? null,
          answer: async () => true,
        });
        await act(async () => {});
        expect(text(".a2-proposal-kicker")).toBe(kicker);
        expect(text(".a2-proposal-actions")).toBe(buttons);
        await act(async () => root.unmount());
        adapter.dispose();
      }
    }
    // Kept or forgotten, once answered.
    views["memory:1"].state = "approved";
    views["intention:2"].state = "rejected";
    await mount("fr", one([
      { id: "root", component: "Column", children: ["a", "b"] },
      { id: "a", component: "Proposal", proposalId: "memory:1" },
      { id: "b", component: "Proposal", proposalId: "intention:2" },
    ]), { read: async (r) => views[r] ?? null, answer: async () => true });
    await act(async () => {});
    expect($$(".a2-proposal-kicker").map((n) => n.textContent)).toEqual(["Gardé", "Oublié"]);
  });

  it("answer a proposal of a list, naming the template's component", async () => {
    const view = (ref: string) => ({ ref, card: 1, kind: "memory", state: "pending", text: ref });
    const sent: Answer[] = [];
    await mount("fr", one([
      { id: "root", component: "List", children: { path: "/items", componentId: "item" } },
      { id: "item", component: "Proposal", proposalId: { "@path": "ref" } },
    ], { items: [{ ref: "memory:1" }, { ref: "memory:2" }] }), {
      read: async (r) => view(r),
      answer: async (a) => (sent.push(a), true),
    });
    await act(async () => {});
    expect($$(".a2-proposal-text").map((n) => n.textContent)).toEqual(["memory:1", "memory:2"]);
    await click($$(".a2-proposal-actions button")[2]);
    expect(sent).toEqual([{ surfaceId: "t", componentId: "item", ref: "memory:2", yes: true }]);
  });

  it("mark a proposal drawn from external content with a small badge, the whole sentence for screen readers", async () => {
    const view = { ref: "rule:11", card: 11, state: "pending", text: "Classer la facture ?", tainted: true };
    await mount("fr", one([{ id: "root", component: "Proposal", proposalId: "rule:11" }]), { read: async () => view, answer: async () => true });
    await act(async () => {});
    const badge = $(".a2-proposal-kicker .a2-badge-external");
    expect(badge.getAttribute("title")).toBe("Vient d'un contenu extérieur : vérifie ce qui va être fait.");
    expect(badge.querySelector("[aria-hidden=true]:not(svg)")?.textContent).toBe("extérieur");
    expect(badge.querySelector(".sr-only")?.textContent).toBe("Vient d'un contenu extérieur : vérifie ce qui va être fait.");
    // No more line of its own under the proposal.
    expect($(".a2-proposal-taint")).toBeNull();
  });

  it("say at once that an answer is on its way", async () => {
    const view = { ref: "rule:12", card: 12, state: "pending", text: "Ranger ?" };
    let done!: (ok: boolean) => void;
    await mount("en", one([{ id: "root", component: "Proposal", proposalId: "rule:12" }]), {
      read: async () => view,
      answer: () => new Promise<boolean>((r) => (done = r)),
    });
    await act(async () => {});
    const [yes, no] = $$(".a2-proposal-actions button");
    await act(async () => yes.click());
    expect(yes.classList.contains("is-busy")).toBe(true);
    expect(yes.getAttribute("aria-busy")).toBe("true");
    expect((no as HTMLButtonElement).disabled).toBe(true);
    expect(no.classList.contains("is-busy")).toBe(false);
    await act(async () => done(true));
  });

  it("render Markdown as text, never as HTML", async () => {
    await mount("en", one([
      {
        id: "root",
        component: "Markdown",
        text: "# Title\n\nSome **bold** and *it* and `code`.\n\n- [ok](https://example.org/a)\n- [bad](javascript:alert(1))\n- ![a cat](https://example.org/cat.png)\n\n<img src=x onerror=alert(1)><script>alert(2)</script>",
      },
    ]));
    const md = $(".a2-markdown");
    expect(md.querySelector("h3")?.textContent).toBe("Title");
    expect(md.querySelector("strong")?.textContent).toBe("bold");
    expect(md.querySelector("em")?.textContent).toBe("it");
    expect(md.querySelector("code")?.textContent).toBe("code");
    const links = [...md.querySelectorAll("a")];
    expect(links).toHaveLength(1);
    expect(links[0].getAttribute("href")).toBe("https://example.org/a");
    expect(links[0].getAttribute("target")).toBe("_blank");
    expect(links[0].getAttribute("rel")).toBe("noopener noreferrer");
    expect(links[0].textContent).toBe("ok (opens in a new tab)");
    expect(md.textContent).toContain("bad");
    expect(md.textContent).toContain("a cat");
    expect(md.querySelectorAll("img, script")).toHaveLength(0);
    expect(md.textContent).toContain("<img src=x onerror=alert(1)><script>alert(2)</script>");
    expect(safeHref("mailto:sam@example.org")).toBe("mailto:sam@example.org");
    expect(safeHref("data:text/html,x")).toBeNull();
  });

  it("load an image only through eriphe-core", async () => {
    await mount("fr", one([
      { id: "root", component: "Column", children: ["a", "b", "c", "d"] },
      { id: "a", component: "Image", url: "/espace/media?u=abc", alt: "Un plan", fit: "cover" },
      { id: "b", component: "Image", url: "https://example.org/x.png", alt: "Dehors" },
      { id: "c", component: "Image", url: "data:image/png;base64,AAAA", alt: "Données" },
      { id: "d", component: "Image", url: "//example.org/espace/media?u=abc" },
    ]));
    const imgs = $$("img");
    expect(imgs).toHaveLength(1);
    expect(imgs[0].getAttribute("src")).toBe("/espace/media?u=abc");
    expect(imgs[0].getAttribute("alt")).toBe("Un plan");
    expect(imgs[0].className).toContain("a2-fit-cover");
    expect($$(".a2-image-off").map((n) => n.getAttribute("aria-label"))).toEqual(["Dehors", "Données", "Image non affichée"]);
  });

  it("open a link only on a click, in a new tab, nothing sent with it, its host in clear", async () => {
    await mount("fr", one([
      { id: "root", component: "Column", children: ["a", "b", "list"] },
      { id: "a", component: "Link", label: "Ouvrir la demande", url: "https://example.org/sam/terrasse/changes/12?tab=checks" },
      { id: "b", component: "Link", url: "https://docs.example.org/dalles" },
      { id: "list", component: "List", children: { path: "/pages", componentId: "page" } },
      { id: "page", component: "Link", label: { "@path": "title" }, url: { "@path": "url" } },
    ], { pages: [{ title: "Plan", url: "https://example.org/plan" }] }));
    const links = $$("a.a2-link");
    expect(links).toHaveLength(3);
    for (const a of links) {
      expect(a.getAttribute("target")).toBe("_blank");
      expect(a.getAttribute("rel")).toBe("noopener noreferrer");
      expect(a.getAttribute("referrerpolicy")).toBe("no-referrer");
      expect(a.querySelector(".sr-only")?.textContent).toBe(" (s'ouvre dans un nouvel onglet)");
    }
    expect(links[0].getAttribute("href")).toBe("https://example.org/sam/terrasse/changes/12?tab=checks");
    expect(links[0].querySelector(".a2-link-label")?.textContent).toBe("Ouvrir la demande");
    expect(links[0].querySelector(".a2-link-host")?.textContent).toBe("example.org");
    // No label: the whole address.
    expect(links[1].querySelector(".a2-link-label")).toBeNull();
    expect(links[1].querySelector(".a2-link-host")?.textContent).toBe("https://docs.example.org/dalles");
    // Bound in a template, to a field that is an address.
    expect(links[2].getAttribute("href")).toBe("https://example.org/plan");
    expect(links[2].querySelector(".a2-link-label")?.textContent).toBe("Plan");
    expect(actions).toEqual([]);
  });

  it("show no link for an address that is not a web page's", async () => {
    const bad = ["javascript:alert(1)", "data:text/html,x", "https://sam:secret@example.org/", "/espace", "//example.org/a", "https://example.org/a b", "https://example.org\\@evil.example.org/"];
    await mount("fr", one([
      { id: "root", component: "Column", children: bad.map((_, i) => `l${i}`) },
      ...bad.map((url, i) => ({ id: `l${i}`, component: "Link", label: `Lien ${i}`, url })),
    ]));
    expect($$("a")).toHaveLength(0);
    expect($$(".a2-link-off").map((n) => n.textContent)).toEqual(bad.map((_, i) => `Lien ${i}`));
  });

  it("show a link's whole address on a surface showing external content", async () => {
    const metadata = { extensions: { eriphe: { title: "Revue", tainted: true } } };
    const comps = [{ id: "root", component: "Link", label: "Ouvrir", url: "https://example.org/a?b=c" }];
    await mount("fr", [{ version: "1.0", createSurface: { surfaceId: "t", catalogId: CATALOG.id, components: comps, dataModel: {}, metadata } }]);
    expect(text(".a2-link-label")).toBe("Ouvrir");
    expect(text(".a2-link-host")).toBe("https://example.org/a?b=c");
  });

  it("show a unified diff line by line, each line its kind by class", async () => {
    const diff = [
      "diff --git a/plan.md b/plan.md",
      "index 3b18e51..a9c2f04 100644",
      "--- a/plan.md",
      "+++ b/plan.md",
      "@@ -1,3 +1,3 @@",
      " Titre",
      "-Dalles : 24",
      "+Dalles : 26",
      "--- vieille note",
      "+++ nouvelle note",
      "\\ No newline at end of file",
      "@@ -10 +10,0 @@",
      "-fin",
      "",
    ].join("\n");
    await mount("fr", one([
      { id: "root", component: "Column", children: ["d", "c"] },
      { id: "d", component: "Code", mode: "diff", text: { "@path": "/diff" } },
      { id: "c", component: "Code", language: "go", text: "func main() {\n\tprintln(1)\n}\n" },
    ], { diff }));
    const kinds = $$(".a2-code-diff .a2-code-line").map((l) => [l.className.replace("a2-code-line a2-diff-", ""), l.textContent]);
    expect(kinds).toEqual([
      ["file", "diff --git a/plan.md b/plan.md"],
      ["file", "index 3b18e51..a9c2f04 100644"],
      ["file", "--- a/plan.md"],
      ["file", "+++ b/plan.md"],
      ["hunk", "@@ -1,3 +1,3 @@"],
      ["ctx", " Titre"],
      ["del", "-Dalles : 24"],
      ["add", "+Dalles : 26"],
      ["del", "--- vieille note"],
      ["add", "+++ nouvelle note"],
      ["note", "\\ No newline at end of file"],
      ["hunk", "@@ -10 +10,0 @@"],
      ["del", "-fin"],
    ]);
    // Code as it is: its lines, its language, no diff class.
    const code = $(".a2-code-code");
    expect(code.querySelector("figcaption.a2-code-lang")?.textContent).toBe("go");
    expect([...code.querySelectorAll(".a2-code-line")].map((l) => [l.className, l.textContent])).toEqual([
      ["a2-code-line", "func main() {"],
      ["a2-code-line", "\tprintln(1)"],
      ["a2-code-line", "}"],
    ]);
    expect(document.querySelectorAll("[style]")).toHaveLength(0);
  });

  it("draw its icons inline, and nothing for a name it does not know", async () => {
    await mount("fr", one([
      { id: "root", component: "Row", children: ["a", "b", "c"] },
      { id: "a", component: "Icon", name: "mail" },
      { id: "b", component: "Icon", name: { "@path": "/icon" } },
      { id: "c", component: "Button", label: "Chercher", icon: "search", action: { event: { name: "ask", context: { text: "x" } } } },
    ], { icon: "nothing" }));
    expect($$(".a2-row > svg.a2-icon")).toHaveLength(1);
    expect($$("button svg.a2-icon path").length).toBeGreaterThan(0);
  });
});

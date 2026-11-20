// @vitest-environment jsdom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { motionValue } from "motion/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { Boot, GraphNode, OrbFeed } from "../data/api";
import { empty, store } from "../data/store";
import type { LNode } from "../graph/layout";
import { makeT, WordsContext } from "../i18n";
import strings from "../strings.json";
import { Bar } from "./Bar";
import { Orb } from "./Orb";
import { resetOrb } from "./useOrb";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const node = (id: string, title: string, more: Partial<GraphNode> = {}): GraphNode => ({
  id, title, lifetime: "work", state: "idle", created: "2026-11-18T10:00:00Z", updated: "2026-11-18T10:00:00Z", version: 1, ...more,
});
const nodes = { sub: node("sub", "Substack"), mail: node("mail", "Urgent : la prod", { titleExternal: true }) };
const bodies: LNode[] = [
  { id: "sub", links: [], r: 30, lw: 120, lh: 44 },
  { id: "mail", links: [], r: 30, lw: 160, lh: 44 },
];
const at = { sub: [0, 0] as [number, number], mail: [400, 0] as [number, number] };
const boot = { lang: "fr", name: "Eriphe", csrf: "", strings: {}, live: true } as Boot;

let reduce = false;
let el: HTMLDivElement;
let root: Root;
const draw = async (narrow = false) =>
  act(async () =>
    root.render(
      <WordsContext.Provider value={{ t: makeT(strings.fr), lang: "fr" }}>
        {!narrow && <Orb bodies={bodies} at={at} cam={[motionValue(300), motionValue(300), motionValue(1)]} onOpen={(id) => store.setState({ open: [id], talk: id })} />}
        <Bar boot={boot} narrow={narrow} />
      </WordsContext.Provider>,
    ),
  );
const feed = (orb: OrbFeed) => act(async () => store.setState({ orb }));
const say = () => el.querySelector(".orb-say");
const orb = () => el.querySelector<HTMLButtonElement>("button.orb")!;
const live = () => el.querySelector('[aria-live="polite"]')?.textContent;

beforeEach(() => {
  vi.useFakeTimers();
  vi.stubGlobal("matchMedia", (q: string) => ({ matches: reduce && q.includes("reduce"), addEventListener() {}, removeEventListener() {} }));
  vi.stubGlobal("requestAnimationFrame", () => 0);
  vi.stubGlobal("cancelAnimationFrame", () => undefined);
  localStorage.clear();
  resetOrb();
  store.setState({ ...empty(), nodes });
  el = document.createElement("div");
  document.body.append(el);
  root = createRoot(el);
});
afterEach(() => {
  act(() => root.unmount());
  el.remove();
  vi.useRealTimers();
  vi.unstubAllGlobals();
  reduce = false;
});

describe("the orb", () => {
  it("rests in the bar, then says beside its subject what it did, for a while", async () => {
    await draw();
    expect(orb().classList.contains("docked")).toBe(true);
    expect(say()?.textContent).toBe("");
    await feed({ asked: 4, answer: { seq: 5, node: "sub", text: "J'ai ouvert le sujet.\nLe plan est écrit.", more: true } });
    expect(orb().classList.contains("docked")).toBe(false);
    expect(orb().getAttribute("aria-label")).toBe("Eriphe, à côté de Substack : l'ouvrir");
    expect(say()?.textContent).toContain("J'ai ouvert le sujet.");
    expect(live()).toBe("J'ai ouvert le sujet.\nLe plan est écrit.");
    // « Voir plus » opens its page: the rest is there, not in the orb
    await act(async () => [...say()!.querySelectorAll("button")].find((b) => b.textContent === "Voir plus")!.click());
    expect(store.getState().open).toEqual(["sub"]);
    // its while over, it goes back to the bar
    await act(async () => vi.advanceTimersByTime(16_000));
    expect(orb().classList.contains("docked")).toBe(true);
  });

  it("holds an answer given at the home whole until it is closed, a reload too", async () => {
    const home: OrbFeed = { asked: 40, answer: { seq: 41, text: "Oui, je suis branché sur **Seerr**.\n\nChaque recherche te demande ton accord.", whole: true } };
    await feed(home);
    await draw();
    expect(say()?.querySelector("strong")?.textContent).toBe("Seerr");
    await act(async () => vi.advanceTimersByTime(60_000));
    expect(say()?.textContent).toContain("Chaque recherche te demande ton accord.");
    // an event's words wait behind it
    await feed({ ...home, said: { seq: 43, text: "Un outil de plus : films." } });
    expect(say()?.textContent).not.toContain("films");
    await act(async () => (say()!.querySelector("button.orb-x") as HTMLButtonElement).click());
    expect(say()?.textContent).toContain("Un outil de plus : films.");
    // closed for good: not even a reload brings it back
    act(() => root.unmount());
    resetOrb();
    root = createRoot(el);
    await draw();
    expect(say()?.textContent).not.toContain("Seerr");
  });

  it("lets the home's answer go when a subject opens", async () => {
    await feed({ asked: 40, answer: { seq: 41, text: "Un.\n\nDeux.", whole: true } });
    await draw();
    expect(say()?.textContent).toContain("Deux.");
    await act(async () => store.setState({ open: ["sub"], talk: "sub" }));
    expect(say()?.textContent).not.toContain("Deux.");
  });

  it("works beside its subject as soon as the user writes", async () => {
    await draw();
    await act(async () => store.setState({ talk: "sub", sent: { text: "Et alors ?", after: 0, to: "sub" } }));
    expect(orb().classList.contains("working")).toBe(true);
    expect(orb().getAttribute("aria-label")).toBe("Eriphe travaille sur Substack…");
    expect(say()?.querySelector(".orb-dots")).not.toBeNull();
    await feed({ work: { node: "sub", draft: "Je regarde" } });
    expect(say()?.textContent).toBe("Je regarde");
  });

  it("points at the card that waits, and says when it could not answer, without its error", async () => {
    await draw();
    await feed({ asked: 2, wait: { card: 7, node: "mail" } });
    expect(orb().getAttribute("aria-label")).toBe("Eriphe, à côté de Urgent : la prod ⚠ : l'ouvrir");
    expect(say()?.textContent).toContain("Il me faut ton oui ici.");
    await feed({ asked: 3, failed: { seq: 4 } });
    expect(say()?.querySelector('[role="alert"]')?.textContent).toContain("n'a pas pu répondre");
    expect(say()?.textContent).toContain("Réessayer");
  });

  it("marks words written after an external read, never replaces them", async () => {
    await draw();
    await feed({ asked: 2, answer: { seq: 3, node: "mail", text: "Le mail demande un virement.", external: true } });
    expect(say()?.textContent).toContain("Le mail demande un virement.");
    expect(say()?.querySelector(".orb-ext")?.getAttribute("title")).toBe("Écrit après avoir lu un contenu extérieur");
  });

  it("stays in the bar and says it in a line above it when the space has no free spot", async () => {
    // one bubble so big it fills the screen: nowhere to go without covering it
    const huge: LNode[] = [{ id: "sub", links: [], r: 4000, lw: 120, lh: 44 }];
    await act(async () =>
      root.render(
        <WordsContext.Provider value={{ t: makeT(strings.fr), lang: "fr" }}>
          <Orb bodies={huge} at={at} cam={[motionValue(300), motionValue(300), motionValue(1)]} onOpen={() => undefined} />
          <Bar boot={boot} narrow={false} />
        </WordsContext.Provider>,
      ),
    );
    await feed({ asked: 4, answer: { seq: 5, node: "sub", text: "C'est fait." } });
    expect(orb().classList.contains("docked")).toBe(true);
    expect(say()?.classList.contains("on")).toBe(false);
    const line = el.querySelector(".orb-line")!;
    expect(line.textContent).toContain("C'est fait.");
    expect(line.querySelector(".orb-on")?.textContent).toBe("Sur Substack");
    expect(line.querySelector(".orb-mini")).toBeNull();
  });

  it("glides to its place, or goes at once when less motion is asked", async () => {
    const place = async () => {
      await draw();
      const docked = orb().style.transform;
      await feed({ asked: 4, answer: { seq: 5, node: "sub", text: "Fait." } });
      return [docked, orb().style.transform];
    };
    const [from, glided] = await place();
    // one frame on its way: not there yet, its words not shown
    expect(glided).not.toBe(from);
    expect(say()?.classList.contains("on")).toBe(false);
    act(() => root.unmount());
    resetOrb();
    store.setState({ ...empty(), nodes });
    root = createRoot(el);
    reduce = true;
    await place();
    // there at once, its words shown
    expect(say()?.classList.contains("on")).toBe(true);
  });
});

describe("the bar", () => {
  it("shows the messages that wait for Eriphe, a clock each, and withdraws one", async () => {
    const calls: string[] = [];
    vi.stubGlobal("fetch", async (url: string, init?: RequestInit) => {
      calls.push(`${init?.method} ${url}`);
      return new Response(null, { status: url.endsWith("/43") ? 409 : 204 });
    });
    const waiting = [
      { seq: 41, text: "Et pour le budget ?" },
      { seq: 42, text: "Et le planning ?", node: "k3x9m2p7qa", title: "Substack" },
      { seq: 43, text: "Et les devis ?" },
    ];
    await feed({ asked: 40, work: {}, waiting });
    await draw();
    const lines = () => [...el.querySelectorAll(".cs-wait")];
    expect(lines().map((l) => l.querySelector(".cs-wait-text")?.textContent)).toEqual(["Et pour le budget ?", "Et le planning ?", "Et les devis ?"]);
    expect(lines().every((l) => l.querySelector(".cs-wait-clock"))).toBe(true);
    expect(lines().map((l) => l.querySelector(".cs-wait-node")?.textContent ?? null)).toEqual([null, "Substack", null]);
    await act(async () => (lines()[1].querySelector(".cs-wait-x") as HTMLButtonElement).click());
    expect(calls).toEqual(["DELETE /messages/42"]);
    expect(lines()).toHaveLength(2);
    // Eriphe read it already: it stays, and the bar says so.
    await act(async () => (lines()[1].querySelector(".cs-wait-x") as HTMLButtonElement).click());
    expect(lines()).toHaveLength(2);
    expect(el.querySelector('.cs-note[role="status"]')?.textContent).toContain("déjà lu");
    await feed({ asked: 43, work: {} });
    expect(el.querySelector(".cs-waiting")).toBeNull();
  });

  it("shows the newest three that wait, and counts the others", async () => {
    await feed({ work: {}, waiting: [1, 2, 3, 4, 5].map((n) => ({ seq: 50 + n, text: `Message ${n}` })) });
    await draw();
    expect([...el.querySelectorAll(".cs-wait-text")].map((l) => l.textContent)).toEqual(["Message 3", "Message 4", "Message 5"]);
    expect(el.querySelector(".cs-wait-more")?.textContent).toBe("+2 en attente");
  });

  it("is a simple line on the phone: the subject named, the words, no gliding", async () => {
    await feed({ asked: 4, answer: { seq: 5, node: "sub", text: "C'est fait." } });
    await draw(true);
    expect(el.querySelector("button.orb")).toBeNull();
    const line = el.querySelector(".orb-line")!;
    expect(line.querySelector(".orb-on")?.textContent).toBe("Sur Substack");
    expect(line.textContent).toContain("C'est fait.");
    await act(async () => (line.querySelector(".orb-on") as HTMLButtonElement).click());
    expect(store.getState().open).toEqual(["sub"]);
  });
});

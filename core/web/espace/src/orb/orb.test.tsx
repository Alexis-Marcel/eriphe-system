// @vitest-environment jsdom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { motionValue } from "motion/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { Boot, GraphNode, OrbFeed } from "../data/api";
import { applyShow, empty, store } from "../data/store";
import type { LNode } from "../graph/layout";
import { makeT, WordsContext } from "../i18n";
import strings from "../strings.json";
import { Bar } from "./Bar";
import { Orb } from "./Orb";
import { OrbProvider, resetOrb } from "./useOrb";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const node = (id: string, title: string, more: Partial<GraphNode> = {}): GraphNode => ({
  id, title, status: "idle", created: "2026-11-18T10:00:00Z", updated: "2026-11-18T10:00:00Z", version: 1, ...more,
});
const nodes = { sub: node("sub", "Substack"), mail: node("mail", "Urgent : la prod", { titleExternal: true }) };
const bodies: LNode[] = [
  { id: "sub", links: [], r: 30, lw: 120, lh: 44 },
  { id: "mail", links: [], r: 30, lw: 160, lh: 44 },
];
const at = { sub: [0, 0] as [number, number], mail: [400, 0] as [number, number] };
const boot = { lang: "fr", name: "Eriphe", csrf: "", strings: {}, live: true, attach: { many: 3, size: 1000 } } as Boot;

let reduce = false;
let hidden = false;
let el: HTMLDivElement;
let root: Root;
const draw = async (narrow = false) =>
  act(async () =>
    root.render(
      <WordsContext.Provider value={{ t: makeT(strings.fr), lang: "fr" }}><OrbProvider>
        {!narrow && <Orb bodies={bodies} at={(id) => at[id as keyof typeof at]} onFrame={() => () => undefined} cam={[motionValue(300), motionValue(300), motionValue(1)]} onOpen={(id) => store.setState({ open: [id], talk: id })} />}
        <Bar boot={boot} narrow={narrow} />
      </OrbProvider></WordsContext.Provider>,
    ),
  );
const feed = (orb: OrbFeed) => act(async () => store.setState({ orb }));
const say = () => el.querySelector(".orb-say");
const orb = () => el.querySelector<HTMLButtonElement>("button.orb")!;
const live = () => el.querySelector('[aria-live="polite"]')?.textContent;
const wait = (ms: number) => act(async () => vi.advanceTimersByTime(ms));
const tab = (h: boolean) =>
  act(async () => {
    hidden = h;
    document.dispatchEvent(new Event("visibilitychange"));
  });

beforeEach(() => {
  vi.useFakeTimers();
  vi.stubGlobal("matchMedia", (q: string) => ({ matches: reduce && q.includes("reduce"), addEventListener() {}, removeEventListener() {} }));
  vi.stubGlobal("requestAnimationFrame", () => 0);
  vi.stubGlobal("cancelAnimationFrame", () => undefined);
  Object.defineProperty(document, "visibilityState", { configurable: true, get: () => (hidden ? "hidden" : "visible") });
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
  delete (document as { visibilityState?: unknown }).visibilityState;
  reduce = false;
  hidden = false;
});

describe("the orb", () => {
  it("rests in the bar, then says beside its subject what it did, until something else happens", async () => {
    await draw();
    expect(orb().classList.contains("docked")).toBe(true);
    expect(say()?.textContent).toBe("");
    await feed({ asked: 4, answer: { seq: 5, node: "sub", text: "J'ai ouvert le sujet.\nLe plan est écrit.", more: true, full: "J'ai ouvert le sujet.\n\nLe plan est écrit.\n\n- **Étape un**\n- Étape deux" } });
    expect(orb().classList.contains("docked")).toBe(false);
    expect(orb().getAttribute("aria-label")).toBe("Eriphe, à côté de Substack : l'ouvrir");
    expect(say()?.textContent).toContain("J'ai ouvert le sujet.");
    expect(live()).toBe("J'ai ouvert le sujet.\nLe plan est écrit.");
    // two minutes of nothing else, it goes back to the bar
    await wait(120_000);
    expect(orb().classList.contains("docked")).toBe(true);
  });

  it("unfolds its whole answer in place on « Voir plus », and keeps it until closed", async () => {
    await draw();
    await feed({ asked: 4, answer: { seq: 5, node: "sub", text: "J'ai ouvert le sujet.\nLe plan est écrit.", more: true, full: "J'ai ouvert le sujet.\n\nLe plan est écrit.\n\n- **Étape un**\n- Étape deux" } });
    expect(say()?.textContent).not.toContain("Étape deux");
    await act(async () => [...say()!.querySelectorAll("button")].find((b) => b.textContent === "Voir plus")!.click());
    expect(say()?.querySelector(".orb-answer strong")?.textContent).toBe("Étape un");
    expect(say()?.textContent).toContain("Étape deux");
    expect([...say()!.querySelectorAll("button")].some((b) => b.textContent === "Voir plus")).toBe(false);
    // unfolded, it does not go by itself
    await wait(180_000);
    expect(say()?.textContent).toContain("Étape deux");
    await act(async () => (say()!.querySelector("button.orb-x") as HTMLButtonElement).click());
    expect(orb().classList.contains("docked")).toBe(true);
  });

  it("lets a newer answer replace its words, for good", async () => {
    await draw();
    await feed({ asked: 4, answer: { seq: 5, node: "sub", text: "C'est fait." } });
    await wait(20_000);
    expect(say()?.textContent).toContain("C'est fait.");
    await feed({ asked: 6, answer: { seq: 7, node: "mail", text: "Un mail est arrivé." } });
    expect(say()?.textContent).toContain("Un mail est arrivé.");
    expect(orb().getAttribute("aria-label")).toBe("Eriphe, à côté de Urgent : la prod ⚠ : l'ouvrir");
    // closed, the words they replaced do not come back
    await act(async () => (say()!.querySelector("button.orb-x") as HTMLButtonElement).click());
    expect(orb().classList.contains("docked")).toBe(true);
  });

  it("stays in the bar when a subject begins to wait for the user, unasked", async () => {
    await draw();
    await feed({ asked: 4 });
    await act(async () => store.setState({ nodes: { ...store.getState().nodes, sub: { ...store.getState().nodes.sub, status: "waits" } } }));
    expect(orb().classList.contains("docked")).toBe(true);
    expect(say()?.textContent ?? "").not.toContain("t'attend");
  });

  it("moves on from its words when the user opens another subject", async () => {
    await draw();
    await feed({ asked: 4, answer: { seq: 5, node: "sub", text: "C'est fait." } });
    await act(async () => store.setState({ open: ["mail"], talk: "mail" }));
    expect(orb().classList.contains("docked")).toBe(true);
    // the subject they are about, opened: they stay beside its page
    await feed({ asked: 6, answer: { seq: 7, node: "sub", text: "Et le plan." } });
    await act(async () => store.setState({ open: ["sub"], talk: "sub" }));
    expect(say()?.textContent).toContain("Et le plan.");
  });

  it("moves on from its words when the user writes", async () => {
    await draw();
    const x: OrbFeed = { asked: 4, answer: { seq: 5, node: "sub", text: "C'est fait." } };
    await feed(x);
    await act(async () => store.setState({ sent: { text: "Merci.", after: 4, to: null } }));
    expect(orb().classList.contains("working")).toBe(true);
    // the message read, no answer yet: the old words do not come back
    await act(async () => store.setState({ sent: null, orb: { ...x, asked: 6 } }));
    expect(orb().classList.contains("docked")).toBe(true);
  });

  it("counts its two minutes only while the tab is seen", async () => {
    await draw();
    await feed({ asked: 4, answer: { seq: 5, node: "sub", text: "C'est fait." } });
    await wait(60_000);
    await tab(true);
    await wait(10 * 60_000);
    await tab(false);
    expect(say()?.textContent).toContain("C'est fait.");
    await wait(59_000);
    expect(say()?.textContent).toContain("C'est fait.");
    await wait(1_000);
    expect(orb().classList.contains("docked")).toBe(true);
  });

  it("holds an answer given at the home whole until it is closed, a reload too", async () => {
    const home: OrbFeed = { asked: 40, answer: { seq: 41, text: "Oui, je suis branché sur **Seerr**.\n\nChaque recherche te demande ton accord.", whole: true } };
    await feed(home);
    await draw();
    expect(say()?.querySelector("strong")?.textContent).toBe("Seerr");
    await act(async () => vi.advanceTimersByTime(60_000));
    expect(say()?.textContent).toContain("Chaque recherche te demande ton accord.");
    await act(async () => (say()!.querySelector("button.orb-x") as HTMLButtonElement).click());
    expect(orb().classList.contains("docked")).toBe(true);
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

  it("says the answer that raised a card as any other, never a placeholder, wherever the card waits", async () => {
    await draw();
    // the answer belongs to Substack, the card waits in the mail's page: the orb says the answer beside Substack
    await feed({ asked: 2, answer: { seq: 3, node: "sub", text: "J'ai lancé le tri : dis oui sur sa carte." }, cards: [] });
    expect(orb().getAttribute("aria-label")).toBe("Eriphe, à côté de Substack : l'ouvrir");
    expect(say()?.textContent).toContain("J'ai lancé le tri : dis oui sur sa carte.");
    expect(say()?.textContent).not.toContain("Il me faut ton oui ici.");
    expect(say()?.querySelectorAll("button.orb-link")).toHaveLength(0);
    // Eriphe opens the card's page beside it (a show: the bar still talks to Substack): the answer stays
    await act(async () => store.setState({ talk: "sub" }));
    await act(async () => store.setState(applyShow(store.getState(), { node: "mail", seq: 1, at: new Date().toISOString() })));
    expect(store.getState().open).toContain("mail");
    expect(say()?.textContent).toContain("J'ai lancé le tri : dis oui sur sa carte.");
  });

  it("says when it could not answer, without its error", async () => {
    await draw();
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
        <WordsContext.Provider value={{ t: makeT(strings.fr), lang: "fr" }}><OrbProvider>
          <Orb bodies={huge} at={(id) => at[id as keyof typeof at]} onFrame={() => () => undefined} cam={[motionValue(300), motionValue(300), motionValue(1)]} onOpen={() => undefined} />
          <Bar boot={boot} narrow={false} />
        </OrbProvider></WordsContext.Provider>,
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

  it("attaches files, says those it cannot take, and sends them with the message", async () => {
    const sent: FormData[] = [];
    let refuse = "";
    vi.stubGlobal("fetch", async (_url: string, init?: RequestInit) => {
      if (init?.body instanceof FormData) sent.push(init.body);
      return refuse ? new Response(refuse, { status: 400 }) : new Response(null, { status: 204 });
    });
    let made = 0;
    const revoked: string[] = [];
    vi.stubGlobal("URL", Object.assign(Object.create(URL), { createObjectURL: () => `blob:${++made}`, revokeObjectURL: (u: string) => revoked.push(u) }));
    await act(async () => store.setState({ talk: "sub" }));
    await draw();
    const picker = el.querySelector<HTMLInputElement>('input[type="file"]')!;
    expect(picker.multiple).toBe(true);
    expect(picker.accept).toContain("image/*");
    expect(picker.accept).toContain("application/pdf");
    const pick = (...files: File[]) =>
      act(async () => {
        Object.defineProperty(picker, "files", { configurable: true, value: files });
        picker.dispatchEvent(new Event("change", { bubbles: true }));
      });
    const chips = () => [...el.querySelectorAll(".cs-file")].map((c) => c.querySelector(".cs-file-name")?.textContent);
    const img = new File([new Uint8Array([137, 80, 78, 71])], "capture.png", { type: "image/png" });
    await pick(img, new File(["une ligne"], "notes.txt", { type: "text/plain" }), new File(["x".repeat(2000)], "gros.pdf", { type: "application/pdf" }));
    expect(chips()).toEqual(["capture.png", "notes.txt"]);
    expect(el.querySelector(".cs-file img")?.getAttribute("src")).toBe("blob:1");
    expect(el.querySelector('.cs-note[role="alert"]')?.textContent).toContain("« gros.pdf » dépasse");
    // One more than it takes: said, not dropped silently.
    await pick(new File(["a"], "a.md"), new File(["b"], "b.md"));
    expect(chips()).toEqual(["capture.png", "notes.txt", "a.md"]);
    expect(el.querySelector('.cs-note[role="alert"]')?.textContent).toBe("3 pièces jointes au plus par message.");
    // One taken off.
    await act(async () => el.querySelector<HTMLButtonElement>('button[aria-label="Retirer a.md"]')!.click());
    expect(chips()).toEqual(["capture.png", "notes.txt"]);

    // eriphe-core refuses: its words, the files kept.
    refuse = "« notes.txt » n'est pas pris en charge.";
    const submit = () => act(async () => el.querySelector("form.cs-bar")!.dispatchEvent(new Event("submit", { bubbles: true, cancelable: true })));
    await submit();
    expect(el.querySelector('.cs-note[role="alert"]')?.textContent).toBe(refuse);
    expect(chips()).toEqual(["capture.png", "notes.txt"]);

    // Files alone are a message: sent with the node, the chips gone.
    refuse = "";
    await submit();
    const form = sent.at(-1)!;
    expect(form.get("text")).toBe("");
    expect(form.get("node")).toBe("sub");
    expect(form.getAll("files").map((f) => (f as File).name)).toEqual(["capture.png", "notes.txt"]);
    expect(chips()).toEqual([]);
    expect(revoked).toContain("blob:1");
  });

  it("shows the names of the files of a message that waits", async () => {
    await feed({ work: {}, waiting: [{ seq: 60, text: "Regarde ça.", files: ["capture.png", "notes.txt"] }, { seq: 61, text: "", files: ["seul.md"] }] });
    await draw();
    const lines = [...el.querySelectorAll(".cs-wait")];
    expect(lines.map((l) => l.querySelector(".cs-wait-text")?.textContent ?? null)).toEqual(["Regarde ça.", null]);
    expect(lines.map((l) => l.querySelector(".cs-wait-files")?.textContent)).toEqual(["capture.png, notes.txt", "seul.md"]);
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

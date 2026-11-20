// @vitest-environment jsdom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { Boot, Exchange } from "../data/api";
import { store } from "../data/store";
import { makeT, WordsContext } from "../i18n";
import strings from "../strings.json";
import { Asked, Console } from "./Console";
import { ACK_MS, landed, lastSeq, present } from "./exchange";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

describe("the bar's last question", () => {
  it("is the question just sent at once, before eriphe-core has it", () => {
    const before: Exchange = { asked: { seq: 1, text: "Bonjour" }, said: { seq: 2, text: "Bonjour Sam." } };
    const p = present(before, { text: "Garde un œil sur mes mails.", after: 2, to: null });
    expect(p.asked).toBe("Garde un œil sur mes mails.");
    expect(p.writing).toBe(true);
    expect(p.said).toBeNull();
  });

  it("stays above the answer once it landed, and the next question replaces it", () => {
    const answered: Exchange = { asked: { seq: 3, text: "Garde un œil sur mes mails." }, said: { seq: 4, text: "J'ouvre le sujet." } };
    const p = present(answered, null);
    expect(p.asked).toBe("Garde un œil sur mes mails.");
    expect(p.said?.text).toBe("J'ouvre le sujet.");
    expect(present(answered, { text: "Ceux qui demandent une réponse.", after: 4, to: null }).asked).toBe("Ceux qui demandent une réponse.");
  });

  it("lands when eriphe-core keeps it waiting, and the question read stays", () => {
    const busy: Exchange = { asked: { seq: 5, text: "Prends ton temps." }, thinking: true };
    const sent = { text: "Et pour le budget ?", after: lastSeq(busy), to: null };
    expect(landed(busy, sent)).toBe(false);
    const waiting: Exchange = { ...busy, waiting: [{ seq: 6, text: "Et pour le budget ?" }] };
    expect(landed(waiting, sent)).toBe(true);
    expect(present(waiting, null).asked).toBe("Prends ton temps.");
    expect(lastSeq(waiting)).toBe(6);
  });

  it("is none before any question", () => {
    expect(present({ said: { seq: 1, text: "Bonjour." } }, null).asked).toBeNull();
    expect(present(null, null).asked).toBeNull();
  });
});

describe("the bar", () => {
  let el: HTMLDivElement;
  let root: Root;
  const boot = { lang: "fr", name: "Eriphe", csrf: "", strings: {}, live: true } as Boot;
  const draw = async () =>
    act(async () =>
      root.render(
        <WordsContext.Provider value={{ t: makeT(strings.fr), lang: "fr" }}>
          <Console boot={boot} />
        </WordsContext.Provider>,
      ),
    );
  const exchange = (x: Exchange) => act(async () => store.setState({ talk: null, exchange: x }));
  beforeEach(() => {
    vi.useFakeTimers();
    el = document.createElement("div");
    document.body.append(el);
    root = createRoot(el);
  });
  afterEach(() => {
    act(() => root.unmount());
    el.remove();
    vi.useRealTimers();
  });

  it("shows the question and one line of Eriphe's, which goes; never a button of its own", async () => {
    await exchange({ asked: { seq: 10, text: "Écris-moi un article sur le télétravail." }, said: { seq: 11, text: "J'ouvre le sujet Écrire l'article." } });
    await draw();
    expect(el.querySelector(".cs-asked")?.textContent).toContain("Écris-moi un article");
    expect(el.querySelector(".cs-said")?.textContent).toBe("J'ouvre le sujet Écrire l'article.");
    // The question's fold is the only button: no reply to choose, no
    // history to open.
    expect([...el.querySelectorAll("button")].map((b) => b.className)).toEqual(["cs-asked"]);
    await act(async () => vi.advanceTimersByTime(ACK_MS - 100));
    expect(el.querySelector(".cs-said")).not.toBeNull();
    await act(async () => vi.advanceTimersByTime(200));
    expect(el.querySelector(".cs-said")).toBeNull();
    expect(el.querySelector(".cs-asked")?.textContent).toContain("Écris-moi un article");
    // The same line sent again (a reconnect) does not come back.
    await exchange({ asked: { seq: 10, text: "Écris-moi un article sur le télétravail." }, said: { seq: 11, text: "J'ouvre le sujet Écrire l'article." } });
    expect(el.querySelector(".cs-said")).toBeNull();
    // The next answer does.
    await exchange({ asked: { seq: 12, text: "Plus court." }, said: { seq: 13, text: "C'est fait." } });
    expect(el.querySelector(".cs-said")?.textContent).toBe("C'est fait.");
  });

  it("shows what Eriphe is writing, its first line, until its answer lands", async () => {
    await exchange({ asked: { seq: 20, text: "Et la suite ?" }, draft: "Je regarde", thinking: true });
    await draw();
    expect(el.querySelector(".cs-said.draft")?.textContent).toBe("Je regarde");
    await act(async () => vi.advanceTimersByTime(ACK_MS * 2));
    expect(el.querySelector(".cs-said.draft")?.textContent).toBe("Je regarde");
    await exchange({ asked: { seq: 20, text: "Et la suite ?" }, thinking: true });
    expect(el.querySelector(".cs-said")).toBeNull();
    expect(el.querySelector(".cs-dots")).not.toBeNull();
  });

  it("shows the messages that wait for Eriphe, a clock each, and withdraws one", async () => {
    const calls: string[] = [];
    const fetchMock = vi.fn(async (url: string, init?: RequestInit) => {
      calls.push(`${init?.method} ${url}`);
      return new Response(null, { status: url.endsWith("/43") ? 409 : 204 });
    });
    vi.stubGlobal("fetch", fetchMock);
    const waiting = [
      { seq: 41, text: "Et pour le budget ?" },
      { seq: 42, text: "Et le planning ?", node: "k3x9m2p7qa", title: "Substack" },
      { seq: 43, text: "Et les devis ?" },
    ];
    await exchange({ asked: { seq: 40, text: "Prends ton temps." }, thinking: true, waiting });
    await draw();
    const lines = () => [...el.querySelectorAll(".cs-wait")];
    expect(lines().map((l) => l.querySelector(".cs-wait-text")?.textContent)).toEqual(["Et pour le budget ?", "Et le planning ?", "Et les devis ?"]);
    expect(lines().every((l) => l.querySelector(".cs-wait-clock"))).toBe(true);
    // The node it was said to, when not the one the bar talks to.
    expect(lines().map((l) => l.querySelector(".cs-wait-node")?.textContent ?? null)).toEqual([null, "Substack", null]);
    // The last question Eriphe read stays below, in grey.
    expect(el.querySelector(".cs-asked")?.textContent).toContain("Prends ton temps.");

    await act(async () => (lines()[1].querySelector(".cs-wait-x") as HTMLButtonElement).click());
    expect(calls).toEqual(["DELETE /messages/42"]);
    expect(lines().map((l) => l.querySelector(".cs-wait-text")?.textContent)).toEqual(["Et pour le budget ?", "Et les devis ?"]);

    // Eriphe read it already: it stays, and the bar says so.
    await act(async () => (lines()[1].querySelector(".cs-wait-x") as HTMLButtonElement).click());
    expect(calls[1]).toBe("DELETE /messages/43");
    expect(lines()).toHaveLength(2);
    expect(el.querySelector('.cs-note[role="status"]')?.textContent).toContain("déjà lu");

    // Delivered: no clock any more, the bar as before.
    await exchange({ asked: { seq: 43, text: "Et les devis ?" }, thinking: true });
    expect(el.querySelector(".cs-waiting")).toBeNull();
    expect(el.querySelector(".cs-asked")?.textContent).toContain("Et les devis ?");
    vi.unstubAllGlobals();
  });

  it("shows the newest three that wait, and counts the others", async () => {
    const waiting = [1, 2, 3, 4, 5].map((n) => ({ seq: 50 + n, text: `Message ${n}` }));
    await exchange({ thinking: true, waiting });
    await draw();
    expect([...el.querySelectorAll(".cs-wait-text")].map((l) => l.textContent)).toEqual(["Message 3", "Message 4", "Message 5"]);
    expect(el.querySelector(".cs-wait-more")?.textContent).toBe("+2 en attente");
  });

  const films =
    "Oui, je suis branché sur **Seerr** : je peux chercher un film et en demander un. Chaque recherche te demande ton accord. Tu peux la mettre en accès libre dans Réglages → Outils → films.";
  const home: Exchange = { asked: { seq: 40, text: "Tu as accès à mes films ?" }, answer: { seq: 41, text: films } };

  it("holds an answer given at the home whole, under the question, until it is closed", async () => {
    localStorage.clear();
    await exchange(home);
    await draw();
    const answer = () => el.querySelector(".cs-answer");
    expect(answer()?.textContent).toContain("Tu peux la mettre en accès libre dans Réglages → Outils → films.");
    expect(answer()?.querySelector("strong")?.textContent).toBe("Seerr"); // its Markdown
    expect(el.querySelector(".cs-said")).toBeNull(); // no line besides
    const asked = el.querySelector(".cs-asked")!;
    expect(asked.compareDocumentPosition(answer()!) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    // It does not go.
    await act(async () => vi.advanceTimersByTime(ACK_MS * 5));
    expect(answer()).not.toBeNull();
    // A line an event made Eriphe say later shows under it, and goes; the answer stays.
    await exchange({ ...home, said: { seq: 43, text: "Un outil de plus : films." } });
    expect(el.querySelector(".cs-said")?.textContent).toBe("Un outil de plus : films.");
    expect(answer()).not.toBeNull();
    await act(async () => vi.advanceTimersByTime(ACK_MS + 100));
    expect(el.querySelector(".cs-said")).toBeNull();
    expect(answer()?.textContent).toContain("Seerr");
    // ✕ closes it, and it does not come back, not even on a reload.
    await act(async () => (answer()!.querySelector("button.cs-answer-x") as HTMLButtonElement).click());
    expect(answer()).toBeNull();
    expect(el.querySelector(".cs-asked")?.textContent).toContain("Tu as accès à mes films ?");
    await exchange({ ...home });
    expect(answer()).toBeNull();
    act(() => root.unmount());
    root = createRoot(el);
    await draw();
    expect(answer()).toBeNull();
    // The next answer is held again.
    await exchange({ asked: { seq: 50, text: "Et mes séries ?" }, answer: { seq: 51, text: "Aussi, par Sonarr.\n\nLa même chose vaut pour elles." } });
    expect(answer()?.textContent).toContain("La même chose vaut pour elles.");
  });

  it("lets the answer go when the user writes again or opens a subject", async () => {
    localStorage.clear();
    expect(present(home, { text: "Et mes séries ?", after: 41, to: null }).answer).toBeNull();
    await exchange(home);
    await draw();
    expect(el.querySelector(".cs-answer")).not.toBeNull();
    await act(async () => store.setState({ talk: "n-1", exchange: { node: "n-1" } }));
    await act(async () => store.setState({ talk: null, exchange: home }));
    expect(el.querySelector(".cs-answer")).toBeNull();
  });

  it("says when Eriphe could not answer, without its error", async () => {
    await exchange({ asked: { seq: 30, text: "Bonjour" }, failed: true });
    await draw();
    expect(el.querySelector('[role="alert"]')?.textContent).toContain("n'a pas pu répondre");
    expect(el.querySelector(".cs-said")).toBeNull();
  });
});

describe("Asked", () => {
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

  it("is one line, whole on a click, folded again on the next", async () => {
    const long = "Peux-tu regarder mes mails de la semaine et me dire lesquels demandent une réponse avant vendredi ?";
    await act(async () =>
      root.render(
        <WordsContext.Provider value={{ t: makeT(strings.fr), lang: "fr" }}>
          <Asked text={long} />
        </WordsContext.Provider>,
      ),
    );
    const b = el.querySelector("button.cs-asked")!;
    expect(b.textContent).toContain(long);
    expect(b.getAttribute("aria-expanded")).toBe("false");
    expect(b.classList.contains("whole")).toBe(false);
    await act(async () => (b as HTMLButtonElement).click());
    expect(b.getAttribute("aria-expanded")).toBe("true");
    expect(b.classList.contains("whole")).toBe(true);
    await act(async () => (b as HTMLButtonElement).click());
    expect(b.classList.contains("whole")).toBe(false);
  });
});

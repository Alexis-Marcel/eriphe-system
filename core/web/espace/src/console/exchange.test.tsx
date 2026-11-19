// @vitest-environment jsdom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { Boot, Exchange } from "../data/api";
import { store } from "../data/store";
import { makeT, WordsContext } from "../i18n";
import strings from "../strings.json";
import { Asked, Console } from "./Console";
import { ACK_MS, present } from "./exchange";

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

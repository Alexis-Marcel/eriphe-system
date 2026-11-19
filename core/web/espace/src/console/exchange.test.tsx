// @vitest-environment jsdom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import type { Exchange } from "../data/api";
import { makeT, WordsContext } from "../i18n";
import strings from "../strings.json";
import { Asked } from "./Console";
import { present } from "./exchange";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const x = (lines: Exchange["lines"], more: Partial<Exchange> = {}): Exchange => ({ lines, ...more }) as Exchange;

describe("the console's last question", () => {
  it("is the question just sent at once, before eriphe-core has it", () => {
    const before = x([
      { seq: 1, role: "user", text: "Bonjour", time: "" },
      { seq: 2, role: "brain", text: "Bonjour Sam.", time: "" },
    ]);
    const p = present(before, { text: "Garde un œil sur mes mails.", after: 2, to: null });
    expect(p.asked).toBe("Garde un œil sur mes mails.");
    expect(p.writing).toBe(true);
    expect(p.aside).toBeNull();
  });

  it("stays above the answer once it landed, and the next question replaces it", () => {
    const answered = x([
      { seq: 3, role: "user", text: "Garde un œil sur mes mails.", time: "" },
      { seq: 4, role: "brain", text: "Lesquels veux-tu suivre ?", time: "" },
    ]);
    const p = present(answered, null);
    expect(p.asked).toBe("Garde un œil sur mes mails.");
    expect(p.aside).toBe("Lesquels veux-tu suivre ?");
    expect(present(answered, { text: "Ceux qui demandent une réponse.", after: 4, to: null }).asked).toBe("Ceux qui demandent une réponse.");
  });

  it("is none before any question", () => {
    expect(present(x([{ seq: 1, role: "brain", text: "Bonjour.", time: "" }]), null).asked).toBeNull();
    expect(present(null, null).asked).toBeNull();
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

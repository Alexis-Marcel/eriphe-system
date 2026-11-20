// @vitest-environment jsdom
import { act, useState } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import type { PageVersion } from "../data/api";
import { makeT, WordsContext } from "../i18n";
import strings from "../strings.json";
import { Versions } from "./Versions";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

// A page's versions are one chip, the version shown, that opens the list
// of them, the newest first; never a row of buttons, however many.
const many = (n: number): PageVersion[] =>
  Array.from({ length: n }, (_, i) => ({ n: i + 1, author: i % 2 ? "user" : "brain", at: new Date(2026, 10, 20, 9, i).toISOString() }));

// Shown is the page's head as Page holds it: the version shown, the last
// by default.
function Shown({ versions }: { versions: PageVersion[] }) {
  const last = versions.at(-1)?.n ?? 1;
  const [v, setV] = useState(0);
  const shown = v || last;
  return (
    <>
      <Versions versions={versions} shown={shown} last={last} onPick={(n) => setV(n === last ? 0 : n)} />
      <output>{shown}</output>
    </>
  );
}

describe("a page's versions", () => {
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
  const draw = (versions: PageVersion[]) =>
    act(() =>
      root.render(
        <WordsContext.Provider value={{ t: makeT(strings.fr), lang: "fr" }}>
          <Shown versions={versions} />
        </WordsContext.Provider>,
      ),
    );
  const chips = () => el.querySelectorAll<HTMLButtonElement>(".pg-vchip");
  const chip = () => chips()[0];
  const list = () => document.querySelector<HTMLElement>(".pg-vlist");
  const options = () => [...document.querySelectorAll<HTMLButtonElement>(".pg-vlist [role='option']")];
  const key = (to: Element, k: string) => act(() => void to.dispatchEvent(new KeyboardEvent("keydown", { key: k, bubbles: true })));

  it("folds 17 versions into one chip, the last shown", () => {
    draw(many(17));
    expect(chips()).toHaveLength(1);
    expect(el.querySelectorAll("button")).toHaveLength(1);
    expect(chip().textContent).toBe("v17");
    expect(chip().classList.contains("old")).toBe(false);
    expect(chip().getAttribute("aria-haspopup")).toBe("listbox");
    expect(chip().getAttribute("aria-expanded")).toBe("false");
    expect(chip().title).toBe("Version 17, la dernière : voir les autres");
    expect(list()).toBeNull();
  });

  it("opens the list, newest first, with who and when", () => {
    draw(many(17));
    act(() => chip().click());
    expect(chip().getAttribute("aria-expanded")).toBe("true");
    expect(list()?.getAttribute("role")).toBe("listbox");
    const o = options();
    expect(o.map((b) => b.querySelector("b")?.textContent)).toEqual(Array.from({ length: 17 }, (_, i) => `v${17 - i}`));
    expect(o[0].querySelector(".pg-vwho")?.textContent).toBe("Eriphe");
    expect(o[1].querySelector(".pg-vwho")?.textContent).toBe("toi");
    expect(o[0].querySelector("em")?.textContent).toBe("dernière");
    expect(o[1].querySelector("em")).toBeNull();
    expect(o[0].querySelector("time")?.textContent).not.toBe("");
    expect(o[0].getAttribute("aria-selected")).toBe("true");
    expect(document.activeElement).toBe(o[0]);
    // A file changed outside is not Eriphe's; a task's and code's are.
    draw([...many(3), { n: 4, author: "outside", at: "" }, { n: 5, author: "task:t1", at: "" }, { n: 6, author: "eriphe", at: "" }]);
    expect(options().map((b) => b.querySelector(".pg-vwho")?.textContent)).toEqual(["Eriphe", "Eriphe", "extérieur", "Eriphe", "toi", "Eriphe"]);
  });

  it("shows v5 when chosen, the chip marked as not the last", () => {
    draw(many(17));
    act(() => chip().click());
    act(() => options().find((b) => b.querySelector("b")?.textContent === "v5")!.click());
    expect(el.querySelector("output")?.textContent).toBe("5");
    expect(list()).toBeNull();
    expect(chip().textContent).toBe("v5");
    expect(chip().classList.contains("old")).toBe(true);
    expect(chip().title).toBe("Version 5, pas la dernière (v17) : voir les autres");
    expect(document.activeElement).toBe(chip());
    // Open again, v5 is the one marked; the last brings it back.
    act(() => chip().click());
    expect(options().filter((b) => b.getAttribute("aria-selected") === "true").map((b) => b.querySelector("b")?.textContent)).toEqual(["v5"]);
    act(() => options()[0].click());
    expect(chip().textContent).toBe("v17");
    expect(chip().classList.contains("old")).toBe(false);
  });

  it("is moved in with the keyboard, and Escape closes it", () => {
    draw(many(17));
    key(chip(), "ArrowDown");
    expect(list()).not.toBeNull();
    expect(document.activeElement).toBe(options()[0]);
    key(document.activeElement!, "ArrowDown");
    key(document.activeElement!, "ArrowDown");
    expect(document.activeElement).toBe(options()[2]);
    key(document.activeElement!, "End");
    expect(document.activeElement).toBe(options()[16]);
    key(document.activeElement!, "Home");
    expect(document.activeElement).toBe(options()[0]);
    key(document.activeElement!, "Escape");
    expect(list()).toBeNull();
    expect(document.activeElement).toBe(chip());
  });

  it("closes on a click outside", () => {
    draw(many(3));
    act(() => chip().click());
    expect(list()).not.toBeNull();
    act(() => void document.body.dispatchEvent(new Event("pointerdown", { bubbles: true })));
    expect(list()).toBeNull();
  });

  it("is not there with one version", () => {
    draw(many(1));
    expect(chips()).toHaveLength(0);
    draw([]);
    expect(chips()).toHaveLength(0);
  });
});

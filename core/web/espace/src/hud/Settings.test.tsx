// @vitest-environment jsdom
import { act } from "react";
import { createRoot } from "react-dom/client";
import { describe, expect, it } from "vitest";
import { empty, store, viewOf } from "../data/store";
import { DEFAULT_FORCES } from "../graph/layout";
import { makeT, WordsContext } from "../i18n";
import strings from "../strings.json";
import { Settings } from "./Settings";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

// A range input's value as the user sets it: React reads it from the
// native setter.
function slide(input: HTMLInputElement, value: number) {
  Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value")!.set!.call(input, String(value));
  input.dispatchEvent(new Event("input", { bubbles: true }));
}

describe("the workspace's settings", () => {
  it("set the graph's forces and its centring in the store, kept with the view, and give the defaults back", async () => {
    store.setState(empty(), true);
    const el = document.createElement("div");
    document.body.append(el);
    const root = createRoot(el);
    await act(async () =>
      root.render(
        <WordsContext.Provider value={{ t: makeT(strings.fr), lang: "fr" }}>
          <Settings />
        </WordsContext.Provider>,
      ),
    );
    const gear = el.querySelector<HTMLButtonElement>('button[aria-label="Réglages"]')!;
    expect(gear.getAttribute("aria-expanded")).toBe("false");
    await act(async () => gear.click());
    expect(el.querySelectorAll('input[type="range"]')).toHaveLength(4);
    expect(el.querySelector<HTMLAnchorElement>('a[href="/settings"]')?.textContent).toBe("Tous les réglages");
    const reset = [...el.querySelectorAll("button")].find((b) => b.textContent === "Par défaut")!;
    expect(reset.disabled).toBe(true);

    // The repulsion, the second slider.
    await act(async () => slide(el.querySelectorAll<HTMLInputElement>('input[type="range"]')[1], 3000));
    expect(store.getState().forces.repulsion).toBe(3000);
    expect(viewOf(store.getState()).forces).toEqual({ repulsion: 3000 });
    expect(reset.disabled).toBe(false);

    const toggle = el.querySelector<HTMLInputElement>('input[type="checkbox"]')!;
    expect(toggle.checked).toBe(true);
    await act(async () => toggle.click());
    expect(store.getState().centring).toBe(false);
    expect(viewOf(store.getState()).centring).toBe(false);

    await act(async () => reset.click());
    expect(store.getState().forces).toEqual(DEFAULT_FORCES);
    expect(viewOf(store.getState()).forces).toBeUndefined();

    act(() => root.unmount());
    el.remove();
  });
});

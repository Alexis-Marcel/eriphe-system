// @vitest-environment jsdom
import { act } from "react";
import { createRoot } from "react-dom/client";
import { describe, expect, it } from "vitest";
import type { GraphNode } from "../data/api";
import { applyNodes, empty, step, store } from "../data/store";
import { makeT, WordsContext } from "../i18n";
import strings from "../strings.json";
import { List } from "./List";
import { listOrder } from "./model";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const node = (id: string, created: string, parent?: string): GraphNode => ({
  id, title: id, parent, status: "idle", created: `2026-11-18T10:${created}:00Z`, updated: "2026-11-18T10:00:00Z", version: 1,
});

describe("the list, on a small screen", () => {
  it("reads each subject, then its bubbles under it, in the order they were made", () => {
    const nodes = Object.fromEntries(
      [node("b", "02"), node("a", "01"), node("a2", "05", "a"), node("a1", "04", "a"), node("a11", "06", "a1"), node("b1", "03", "b"), node("lost", "07", "gone")].map((x) => [x.id, x]),
    );
    expect(listOrder(nodes).map((x) => x.id)).toEqual(["a", "a1", "a11", "a2", "b", "b1", "lost"]);
  });

  it("says a node waits with its dot only, no word on the row", () => {
    store.setState(empty(), true);
    const waits = { ...node("w", "01"), title: "Devis", status: "waits" as const, line: "Je compare les devis" };
    const runs = { ...node("r", "02"), title: "Recherche", status: "running" as const, line: "Je lis les sources" };
    step(store, applyNodes, { reset: true, nodes: [waits, runs] });
    const el = document.createElement("div");
    document.body.append(el);
    const root = createRoot(el);
    act(() =>
      root.render(
        <WordsContext.Provider value={{ t: makeT(strings.fr), lang: "fr" }}>
          <List live={false} />
        </WordsContext.Provider>,
      ),
    );
    const [w, r] = Array.from(el.querySelectorAll(".ls-row"));
    expect(w.querySelector(".dot.yours")?.getAttribute("aria-label")).toBe(strings.fr["espace.waits"]);
    expect(w.querySelector("em")).toBeNull();
    expect(w.textContent).not.toContain(strings.fr["espace.waits"]);
    expect(r.querySelector("em.runs")?.textContent).toBe("Je lis les sources");
    act(() => root.unmount());
    el.remove();
    store.setState(empty(), true);
  });
});

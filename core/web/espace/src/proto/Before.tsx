import { MotionConfig } from "motion/react";
import { useMemo } from "react";
import { sampleProposals } from "../a2ui/fixtures";
import { ProposalsContext } from "../a2ui/proposals";
import type { NodePage } from "../data/api";
import { samplePage, setViews, type Authored, type SampleView, type Src } from "../data/samplecore";
import { Doc } from "../doc/Doc";
import golden from "../doc/page.golden.json";
import type { State } from "./After";
import { scenarios, type Before, type Scenario } from "./scenarios";

// Today's page, for the comparison: the scenario's « before » blocks, in
// today's catalog, drawn by the real renderer (Doc) through the dev
// server's make-believe core. A node_find source (no items) is handed to
// the block as data, as eriphe-core would after reading it. « habillé »
// is the same page under proto.css's renderer-only polish.

function sampleOf(s: Scenario, state: State): SampleView {
  const values: Record<string, Record<string, unknown>> = {};
  for (const [k, v] of Object.entries(s.values)) {
    const val = v as Record<string, unknown>;
    if (state === "loading") values[k] = {};
    else if (state === "empty") values[k] = Object.fromEntries(Object.keys(val).map((key) => [key, []]));
    else values[k] = val;
  }
  const parts = s.before.map((p) => {
    if (typeof p === "string") return p;
    const b = p as Before;
    const withItems: Record<string, Src> = {};
    const data: Record<string, unknown> = { ...(b.data ?? {}) };
    for (const [name, src] of Object.entries(b.sources ?? {})) {
      if (src.items) withItems[name] = src as Src;
      else data[name] = values[name] ?? {};
    }
    return { ...b, sources: withItems, data } as Authored;
  });
  return { id: `p-${s.id}`, parts, values, notes: s.notes, bodies: {}, cards: s.cards };
}

export function BeforePage({ s, state, polish }: { s: Scenario; state: State; polish: boolean }) {
  const page = useMemo(() => {
    setViews(scenarios.map((x) => sampleOf(x, x.id === s.id ? state : "live")));
    const base = golden as unknown as NodePage;
    const p = samplePage(`p-${s.id}`)!;
    const now = new Date().toISOString();
    return { ...base, node: { id: `p-${s.id}`, title: s.title, lifetime: "standing", status: "idle", created: now, updated: now, version: 1 }, parts: p.parts, cards: p.cards, settled: p.settled } as NodePage;
  }, [s, state]);
  const proposals = useMemo(() => sampleProposals("fr"), []);
  return (
    <ProposalsContext.Provider value={proposals}>
      <MotionConfig reducedMotion="user">
        <div className={polish ? "bv-polish" : "bv-today"}>
          <Doc page={page} latest live={false} />
        </div>
      </MotionConfig>
    </ProposalsContext.Provider>
  );
}

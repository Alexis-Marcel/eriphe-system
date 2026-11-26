import type { ComponentType } from "react";
import type { Proposals } from "./a2ui/proposals";
import { physicsGraph, PhysicsPanel } from "./dev-physics";
import { setReads, type Boot } from "./data/api";
import { sample } from "./data/sample";
import { onBump, sampleOpen } from "./data/samplecore";
import { sampleOrb, sampleProposals } from "./data/sampleproposals";
import { applyNodes, step, store } from "./data/store";
import type { Lang } from "./i18n";
import strings from "./strings.json";

// The dev server's workspace (npm run dev), when no eriphe-core is behind:
// a made-up graph whose pages a make-believe core reads (data/sample.ts,
// samplecore.ts, sampleviews.ts), made-up proposals and orb
// (sampleproposals.ts); ?physique, the graph's forces to tune by hand on
// fixed graphs (dev-physics.tsx). main.tsx imports it only on the dev server: none
// of it is in the built app (a test checks espace.js holds no sample's words).

export type Dev = { proposals: Proposals; start(): () => void; Panel?: ComponentType };

// devBoot is the boot eriphe-core would give, in the language asked
// (?lang=en), else the browser's.
export function devBoot(): Boot {
  const asked = new URLSearchParams(location.search).get("lang");
  const lang: Lang = asked === "en" || (!asked && !navigator.language.startsWith("fr")) ? "en" : "fr";
  return { lang, name: "Eriphe", csrf: "", strings: strings[lang], live: false, attach: { many: 5, size: 20 << 20 } };
}

export function dev(lang: Lang): Dev {
  const physics = new URLSearchParams(location.search).has("physique");
  setReads({
    page: async (id, version) => sample.page(id, version) ?? (physics && store.getState().nodes[id] ? sample.pageOf(store.getState().nodes[id]) : null),
    trace: async (id) => sample.trace(id),
    file: async (id) => sample.file(id),
    open: (id, ref) => sampleOpen(id, ref),
  });
  return {
    proposals: sampleProposals(lang),
    Panel: physics ? PhysicsPanel : undefined,
    start() {
      if (physics) {
        step(store, applyNodes, { reset: true, nodes: physicsGraph() });
        return () => {};
      }
      step(store, applyNodes, { reset: true, nodes: sample.nodes });
      store.setState({ orb: sampleOrb(lang) });
      // A view the make-believe core changed is read again, as eriphe-core's "doc" says.
      return onBump((id) => store.setState((s) => ({ docs: { ...s.docs, [id]: (s.docs[id] ?? 0) + 1 } })));
    },
  };
}

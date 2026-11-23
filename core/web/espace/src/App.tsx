import { useEffect } from "react";
import { MotionConfig } from "motion/react";
import { coreProposals, ProposalsContext } from "./a2ui/proposals";
import { tab, type Boot } from "./data/api";
import type { Dev } from "./dev";
import { startPresence } from "./data/presence";
import { applyTheme, keep } from "./data/keep";
import { applyNotes, openPage, restore, step, store, useGraph } from "./data/store";
import { connect } from "./data/stream";
import { Graph } from "./graph/Graph";
import { setCentring, setForces } from "./graph/layout";
import { List, useNarrow } from "./graph/List";
import { Hud } from "./hud/Hud";
import { Bar } from "./orb/Bar";
import { OrbProvider } from "./orb/useOrb";
import { useWords } from "./i18n";

// The workspace, Eriphe's home page (#633): the graph of nodes, the pages
// open in it, Eriphe's orb, the head-up display over it and the bar under it; on
// a small screen, the graph as a list (graph/List.tsx).
// Nothing here is a screen planned in code: what shows is the graph
// eriphe-core streams, and what the user opens. With no eriphe-core
// behind (the dev server) it shows a made-up graph (dev), and says so.

// openAsked is the node a link asks to open (/espace?open=node-<id>).
export function openAsked(search: string): string | null {
  const m = /^node-([a-z0-9]{1,64})$/.exec(new URLSearchParams(search).get("open") ?? "");
  return m ? m[1] : null;
}

export function App({ boot, dev }: { boot: Boot; dev?: Dev }) {
  useEffect(() => {
    if (dev) return dev.start();
    step(store, restore, boot.view);
    const stop = connect(store);
    const kept = keep(store);
    const quiet = startPresence({ tab, csrf: () => boot.csrf, onNotes: (ns) => step(store, applyNotes, ns) });
    return () => {
      stop();
      kept();
      quiet();
    };
  }, [boot, dev]);

  // A link that names a node opens its page once the graph has it.
  useEffect(() => {
    const id = openAsked(location.search);
    if (!id) return;
    if (store.getState().nodes[id]) return step(store, openPage, id, false);
    const off = store.subscribe((s) => {
      if (!s.nodes[id]) return;
      off();
      step(store, openPage, id, false);
    });
    return off;
  }, []);

  const theme = useGraph((s) => s.theme);
  const narrow = useNarrow();
  useEffect(() => applyTheme(theme), [theme]);
  // The graph's forces and its centring, as set (hud/Settings.tsx) or kept:
  // the graph, warmed, moves to them at once.
  const forces = useGraph((s) => s.forces);
  const centring = useGraph((s) => s.centring);
  useEffect(() => setForces(forces), [forces]);
  useEffect(() => setCentring(centring), [centring]);

  return (
    <ProposalsContext.Provider value={dev?.proposals ?? coreProposals}>
      <MotionConfig reducedMotion="user">
        <OrbProvider>
          <div className="espace">
            {narrow ? <List live={boot.live} /> : <Graph live={boot.live} />}
            <Hud live={boot.live} narrow={narrow} />
            <Bar boot={boot} narrow={narrow} />
            <Sample live={boot.live} />
            {dev?.Panel && <dev.Panel />}
          </div>
        </OrbProvider>
      </MotionConfig>
    </ProposalsContext.Provider>
  );
}

function Sample({ live }: { live: boolean }) {
  const { t } = useWords();
  const empty = useGraph((s) => Object.keys(s.nodes).length === 0);
  if (!live) return <p className="espace-note">{t("espace.sample")}</p>;
  return empty ? <p className="espace-note">{t("espace.empty")}</p> : null;
}

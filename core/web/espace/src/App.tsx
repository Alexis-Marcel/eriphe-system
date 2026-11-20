import { useEffect, useMemo, useState } from "react";
import { MotionConfig } from "motion/react";
import { createAdapter } from "./a2ui/adapter";
import { galleryFixtures, sampleOrb, sampleProposals } from "./a2ui/fixtures";
import { coreProposals, ProposalsContext } from "./a2ui/proposals";
import { Surfaces } from "./a2ui/Surfaces";
import { tab, type Boot } from "./data/api";
import { startPresence } from "./data/presence";
import { sample } from "./data/sample";
import { applyTheme, keep } from "./data/keep";
import { applyNodes, applyNotes, openPage, restore, step, store, useGraph } from "./data/store";
import { connect } from "./data/stream";
import { Graph } from "./graph/Graph";
import { List, useNarrow } from "./graph/List";
import { Hud } from "./hud/Hud";
import { Bar } from "./orb/Bar";
import { useWords } from "./i18n";

// The workspace, Eriphe's home page (#633): the graph of nodes, the pages
// open in it, Eriphe's orb, the head-up display over it and the bar under it; on
// a small screen, the graph as a list (graph/List.tsx).
// Nothing here is a screen planned in code: what shows is the graph
// eriphe-core streams, and what the user opens. With no eriphe-core
// behind (the dev server) it shows a made-up graph, and says so; ?gallery
// shows every component of the catalog instead.

// openAsked is the node a link asks to open (/espace?open=node-<id>).
export function openAsked(search: string): string | null {
  const m = /^node-([a-z0-9]{1,64})$/.exec(new URLSearchParams(search).get("open") ?? "");
  return m ? m[1] : null;
}

export function App({ boot }: { boot: Boot }) {
  const { lang } = useWords();
  const [gallery] = useState(() => new URLSearchParams(location.search).has("gallery"));
  const proposals = useMemo(() => (boot.live ? coreProposals : sampleProposals(lang)), [boot.live, lang]);

  useEffect(() => {
    if (gallery) return;
    if (!boot.live) {
      step(store, applyNodes, { reset: true, nodes: sample.nodes });
      store.setState({ orb: sampleOrb(lang) });
      return;
    }
    step(store, restore, boot.view);
    const stop = connect(store);
    const kept = keep(store);
    const quiet = startPresence({ tab, csrf: () => boot.csrf, onNotes: (ns) => step(store, applyNotes, ns) });
    return () => {
      stop();
      kept();
      quiet();
    };
  }, [boot, gallery, lang]);

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

  if (gallery) return <Gallery />;
  return (
    <ProposalsContext.Provider value={proposals}>
      <MotionConfig reducedMotion="user">
        <div className="espace">
          {narrow ? <List live={boot.live} /> : <Graph live={boot.live} />}
          <Hud live={boot.live} />
          <Bar boot={boot} narrow={narrow} />
          <Sample live={boot.live} />
        </div>
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

// Gallery shows every component of the catalog, one surface under the
// other, to look at them on the dev server.
function Gallery() {
  const { lang } = useWords();
  const [adapter] = useState(() => {
    const a = createAdapter(lang);
    a.applyMessages(galleryFixtures(lang));
    return a;
  });
  return (
    <ProposalsContext.Provider value={sampleProposals(lang)}>
      <div className="gallery">
        <Surfaces adapter={adapter} />
      </div>
    </ProposalsContext.Provider>
  );
}

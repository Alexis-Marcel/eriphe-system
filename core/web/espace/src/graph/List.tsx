import { useEffect, useMemo, useState } from "react";
import { closePage, depthOf, openPage, step, store, talkTo, useGraph } from "../data/store";
import { useWords } from "../i18n";
import { Page } from "../page/Page";
import { listOrder, titleOf, waits } from "./model";

// The workspace on a small screen (#633): the graph as a simple list, each
// subject and its bubbles under it, in the order they were made; a tap
// opens a page, alone, over the list, until it closes; one shown from it
// takes its place, until it closes in turn. The same pages, the
// same console: only the graph gives way.

// NARROW is the screen width under which the list replaces the graph.
export const NARROW = 760; // the width under which the HUD narrows too (espace.css)

export function useNarrow(): boolean {
  const q = `(max-width: ${NARROW}px)`;
  const [narrow, setNarrow] = useState(() => matchMedia(q).matches);
  useEffect(() => {
    const m = matchMedia(q);
    const on = () => setNarrow(m.matches);
    m.addEventListener("change", on);
    return () => m.removeEventListener("change", on);
  }, [q]);
  return narrow;
}

export function List({ live }: { live: boolean }) {
  const { t } = useWords();
  const nodes = useGraph((s) => s.nodes);
  const open = useGraph((s) => s.open);
  const talk = useGraph((s) => s.talk);
  const rows = useMemo(() => listOrder(nodes), [nodes]);
  const external = t("espace.external");
  // One page at a time: the last one opened (one Eriphe shows from a page
  // takes the screen; closed, the one under it comes back), and the bar
  // talks to the page on screen.
  const shown = open.at(-1);
  useEffect(() => {
    if (shown && shown !== talk) step(store, talkTo, shown);
  }, [shown, talk]);
  if (shown && nodes[shown]) {
    return (
      <div className="ls-page">
        <Page id={shown} live={live} talking={false} onTalk={() => undefined} onClose={() => step(store, closePage, shown)} onOpen={(p) => step(store, openPage, p, false)} />
      </div>
    );
  }
  return (
    <nav className="ls" aria-label={t("espace.graph")}>
      <ul>
        {rows.map((x) => {
          const d = depthOf(nodes, x.id);
          const yours = waits(x);
          return (
            <li key={x.id} className={`ls-row d${Math.min(d, 3)} ${x.state}${x.external ? " external" : ""}`}>
              <button type="button" onClick={() => step(store, openPage, x.id, false)}>
                <i className={`dot${yours ? " yours" : x.state === "running" ? " run" : ""}`} />
                <b>{titleOf(x, external)}</b>
                {yours ? <em className="yours">{t("espace.waits")}</em> : x.state === "running" && x.line ? <em className="runs">{x.line}</em> : null}
              </button>
            </li>
          );
        })}
      </ul>
    </nav>
  );
}

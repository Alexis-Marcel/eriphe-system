import { openPage, step, store, useGraph } from "../data/store";
import { titleOf } from "../graph/model";
import { useWords } from "../i18n";
import { useOrb } from "./useOrb";
import { Words } from "./Words";

// Line is the orb on the phone, where the graph is a list: no gliding, a
// simple line above the bar, the subject it is about named first (a tap
// opens it), then what the orb would say beside it.
export function Line() {
  const { t } = useWords();
  const nodes = useGraph((s) => s.nodes);
  const { show, dismiss } = useOrb();
  if (show.mode === "rest") return null;
  const open = (id: string) => step(store, openPage, id, false);
  return (
    <div className={`orb-line ${show.mode}`}>
      <span className={`orb-mini${show.mode === "work" ? " working" : ""}`} aria-hidden="true" />
      {show.node && (
        <button type="button" className="orb-on" onClick={() => open(show.node!)}>
          {t("espace.orb.on", titleOf(nodes[show.node], t("espace.external")))}
        </button>
      )}
      <Words show={show} dismiss={dismiss} open={open} />
    </div>
  );
}

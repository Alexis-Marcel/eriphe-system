import { useState } from "react";
import { AnimatePresence, motion } from "motion/react";
import { Icon } from "../a2ui/icons";
import { setGraphCentring, setGraphForces, step, store, useGraph } from "../data/store";
import { ForcesPanel } from "../graph/Forces";
import { useWords } from "../i18n";

// Settings are the workspace's own settings, under the corner's gear: the
// graph's four forces and whether the open node becomes its centre
// (graph/Forces.tsx), applied at once, the graph moving to them while the
// user sets them; eriphe-core keeps them with the view (keep.ts), for
// every screen. The rest of the settings are a link away (/settings).

export function Settings() {
  const { t } = useWords();
  const [open, setOpen] = useState(false);
  const forces = useGraph((s) => s.forces);
  const centring = useGraph((s) => s.centring);
  return (
    <div className="hud-archives hud-settings">
      <button
        type="button"
        className={`hud-icon${open ? " on" : ""}`}
        aria-expanded={open}
        aria-label={t("espace.settings")}
        title={t("espace.settings")}
        onClick={() => setOpen(!open)}
      >
        <Icon name="settings" />
      </button>
      <AnimatePresence>
        {open && (
          <motion.div
            className="hud-panel"
            role="region"
            aria-label={t("espace.settings")}
            initial={{ opacity: 0, y: -6 }}
            animate={{ opacity: 1, y: 0 }}
            exit={{ opacity: 0, y: -4 }}
            transition={{ duration: 0.2 }}
            onKeyDown={(e) => e.key === "Escape" && setOpen(false)}
          >
            <ForcesPanel value={forces} onChange={(f) => step(store, setGraphForces, f)} centred={centring} onCentred={(on) => step(store, setGraphCentring, on)} />
            <a className="hud-more" href="/settings">
              {t("espace.settings.all")}
            </a>
          </motion.div>
        )}
      </AnimatePresence>
    </div>
  );
}

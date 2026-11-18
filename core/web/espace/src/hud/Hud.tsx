import { useEffect, useState } from "react";
import { AnimatePresence, motion } from "motion/react";
import { Icon } from "../a2ui/icons";
import { localURL } from "../data/presence";
import { dropNote, home, openPage, pathOf, step, store, useGraph } from "../data/store";
import { STREAM_GRACE } from "../data/stream";
import { titleOf, waits } from "../graph/model";
import { useWords } from "../i18n";

// The head-up display (#633): the way home and the path of the page the
// console talks to; the ticker of what waits for the user and what runs,
// each a way to its page; the way to the settings. Under it, the
// notifications routed to this tab, and a word when the stream is down.

const spring = { type: "spring", stiffness: 300, damping: 32 } as const;
const TICKS = 5;

export function Hud({ name, live }: { name: string; live: boolean }) {
  const { t } = useWords();
  const nodes = useGraph((s) => s.nodes);
  const talk = useGraph((s) => s.talk);
  const external = t("espace.external");
  const all = Object.values(nodes);
  const ticks = [...all.filter(waits), ...all.filter((x) => x.state === "running" && !waits(x))].slice(0, TICKS);
  const path = talk ? pathOf(nodes, talk) : [];
  return (
    <>
      <header className="hud">
        <nav className="hud-left" aria-label={t("espace.path")}>
          <button type="button" className="hud-brand" onClick={() => step(store, home)}>
            {name}
          </button>
          {path.map((id, i) => (
            <span key={id} className="hud-crumb">
              <i aria-hidden="true">›</i>
              {i === path.length - 1 ? <b>{titleOf(nodes[id], external)}</b> : <button type="button" onClick={() => step(store, openPage, id, false)}>{titleOf(nodes[id], external)}</button>}
            </span>
          ))}
        </nav>
        <div className="hud-ticker" aria-label={t("espace.ticker")}>
          <AnimatePresence initial={false}>
            {ticks.map((x) => (
              <motion.div key={x.id} layout initial={{ opacity: 0, y: -10 }} animate={{ opacity: 1, y: 0 }} exit={{ opacity: 0, y: -6 }} transition={spring}>
                <button type="button" className={`tick ${waits(x) ? "yours" : "runs"}`} onClick={() => step(store, openPage, x.id, true)}>
                  <i className={`dot ${waits(x) ? "yours" : "run"}`} />
                  <b>{trim(titleOf(x, external))}</b>
                  <span>· {waits(x) ? t("espace.ticker.waits") : (x.line ?? t("espace.ticker.runs"))}</span>
                </button>
              </motion.div>
            ))}
          </AnimatePresence>
        </div>
        <div className="hud-right">
          <a className="hud-icon" href="/settings" aria-label={t("espace.settings")} title={t("espace.settings")}>
            <Icon name="settings" />
          </a>
        </div>
      </header>
      {live && <Offline />}
      <Notices />
    </>
  );
}

const trim = (s: string) => (s.length > 22 ? s.slice(0, 21) + "…" : s);

// Offline says the stream is down, once it has been for a while.
function Offline() {
  const { t } = useWords();
  const up = useGraph((s) => s.live);
  const [late, setLate] = useState(false);
  useEffect(() => {
    if (up) return setLate(false);
    const id = setTimeout(() => setLate(true), STREAM_GRACE);
    return () => clearTimeout(id);
  }, [up]);
  if (up || !late) return null;
  return (
    <p className="hud-offline" role="status">
      {t("espace.offline")} <a href="/conversation">{t("espace.convo")}</a>
    </p>
  );
}

// NOTE_FOR is how long a notification routed here stays on the page.
export const NOTE_FOR = 15_000;

function Notices() {
  const { t, lang } = useWords();
  const notes = useGraph((s) => s.notes);
  useEffect(() => {
    const ids = notes.map((n) => n.id);
    const timers = ids.map((id) => setTimeout(() => step(store, dropNote, id), NOTE_FOR));
    return () => timers.forEach(clearTimeout);
  }, [notes]);
  if (notes.length === 0) return null;
  return (
    <div className="notices" role="region" aria-label={t("espace.notices")}>
      {notes.map((n) => {
        const text = n.body?.[lang] || n.body?.fr || "";
        return (
          <div key={n.id} className="notice" role="alert">
            {localURL(n.url) ? (
              <a className="notice-text" href={n.url}>
                {text}
              </a>
            ) : (
              <span className="notice-text">{text}</span>
            )}
            <button type="button" className="notice-close" aria-label={t("espace.notice.close")} onClick={() => step(store, dropNote, n.id)}>
              ×
            </button>
          </div>
        );
      })}
    </div>
  );
}

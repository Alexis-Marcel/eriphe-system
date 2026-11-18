import { useEffect, useState } from "react";
import { AnimatePresence, motion } from "motion/react";
import { wall, when } from "../a2ui/format";
import { Icon } from "../a2ui/icons";
import { archives, reopen, type ArchivedNode } from "../data/api";
import { localURL } from "../data/presence";
import { dropNote, home, openPage, pathOf, setTheme, step, store, useGraph } from "../data/store";
import { STREAM_GRACE } from "../data/stream";
import { titleOf, waits } from "../graph/model";
import { useWords } from "../i18n";

// The head-up display (#633): the way home and the path of the page the
// console talks to; the ticker of what waits for the user and what runs,
// each a way to its page; what the user archived, to reopen; the light or
// dark theme; the way to the settings. Under it, the notifications routed
// to this tab, and a word when the stream is down.

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
          {live && <Archives />}
          <Theme />
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

// Theme turns the page light or dark; until the user chooses, it follows
// the system. eriphe-core keeps the choice (keep.ts).
function Theme() {
  const { t } = useWords();
  const theme = useGraph((s) => s.theme);
  const dark = theme ? theme === "dark" : matchMedia("(prefers-color-scheme: dark)").matches;
  return (
    <button type="button" className="hud-icon" aria-label={t("espace.theme")} title={t("espace.theme")} aria-pressed={dark} onClick={() => step(store, setTheme, dark ? "light" : "dark")}>
      <span aria-hidden="true">{dark ? "☀" : "☾"}</span>
    </button>
  );
}

// Archives lists what the user archived, the latest first, each to reopen:
// it comes back to the graph with what was archived with it, and opens.
function Archives() {
  const { t, lang } = useWords();
  const [open, setOpen] = useState(false);
  const [list, setList] = useState<ArchivedNode[] | null>(null);
  const size = useGraph((s) => Object.keys(s.nodes).length); // an archive or a reopening changes it
  useEffect(() => {
    let on = true;
    archives().then(
      (xs) => on && setList(xs),
      () => on && setList([]),
    );
    return () => {
      on = false;
    };
  }, [open, size]);
  const back = (id: string) =>
    void reopen(id).then((ok) => {
      if (!ok) return;
      setOpen(false);
      const show = () => store.getState().nodes[id] && (step(store, openPage, id, true), true);
      if (show()) return;
      const off = store.subscribe(() => show() && off());
    });
  const n = list?.length ?? 0;
  const external = t("espace.external");
  return (
    <div className="hud-archives">
      <button type="button" className={`hud-btn${open ? " on" : ""}`} aria-expanded={open} onClick={() => setOpen((o) => !o)}>
        {n ? t("espace.archives.n", n) : t("espace.archives")}
      </button>
      <AnimatePresence>
        {open && (
          <motion.div className="hud-panel" role="region" aria-label={t("espace.archives")} initial={{ opacity: 0, y: -6 }} animate={{ opacity: 1, y: 0 }} exit={{ opacity: 0, y: -4 }} transition={{ duration: 0.2 }}>
            {n === 0 ? (
              <p className="hud-muted">{t("espace.archives.none")}</p>
            ) : (
              <ul className="hud-arch">
                {list!.map((x) => {
                  const at = wall(x.archived);
                  return (
                    <li key={x.id}>
                      <span>
                        <b>{titleOf(x, external)}</b>
                        <em>
                          {at ? t("espace.archives.at", when(lang, at)) : ""}
                          {x.with ? ` · ${x.with === 1 ? t("espace.archives.with1") : t("espace.archives.with", x.with)}` : ""}
                        </em>
                      </span>
                      <button type="button" className="btn" onClick={() => back(x.id)}>
                        {t("espace.archives.reopen")}
                      </button>
                    </li>
                  );
                })}
              </ul>
            )}
          </motion.div>
        )}
      </AnimatePresence>
    </div>
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

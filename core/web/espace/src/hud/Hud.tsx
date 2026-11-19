import { useEffect, useState, type ReactNode } from "react";
import { AnimatePresence, motion } from "motion/react";
import { wall, when } from "../a2ui/format";
import { Icon } from "../a2ui/icons";
import { archives, reopen, type ArchivedNode } from "../data/api";
import { localURL } from "../data/presence";
import { dropNote, openPage, setTheme, step, store, useGraph } from "../data/store";
import { STREAM_GRACE } from "../data/stream";
import { titleOf, waits } from "../graph/model";
import { useWords } from "../i18n";

// The head-up display (#633), with no bar: the graph takes the whole
// screen. Over it, at the top, the ticker of what waits for the user and
// what runs, each a way to its page; in the top-right corner, three icons:
// what the user archived, to reopen; the light or dark theme; the way to
// the settings. Under them, the notifications routed to this tab, and a
// word when the stream is down.

const spring = { type: "spring", stiffness: 300, damping: 32 } as const;
const TICKS = 5;

export function Hud({ live }: { live: boolean }) {
  const { t } = useWords();
  const nodes = useGraph((s) => s.nodes);
  const external = t("espace.external");
  const all = Object.values(nodes);
  const ticks = [...all.filter(waits), ...all.filter((x) => x.state === "running" && !waits(x))].slice(0, TICKS);
  return (
    <>
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
      <div className="hud-corner">
        {live && <Archives />}
        <Theme />
        <a className="hud-icon" href="/settings" aria-label={t("espace.settings")} title={t("espace.settings")}>
          <Icon name="settings" />
        </a>
      </div>
      {live && <Offline />}
      <Notices />
    </>
  );
}

// Glyph draws one of the corner's own icons, in the catalog's stroke
// (.a2-icon): the catalog's set (a2ui/icons.tsx) is what Eriphe may draw in
// a page, these are the workspace's.
function Glyph({ children }: { children: ReactNode }) {
  return (
    <svg className="a2-icon" viewBox="0 0 24 24" aria-hidden="true" focusable="false">
      {children}
    </svg>
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
      <Glyph>
        {dark ? (
          <>
            <circle cx="12" cy="12" r="4" />
            <path d="M12 2.5v2M12 19.5v2M2.5 12h2M19.5 12h2M5.3 5.3l1.4 1.4M17.3 17.3l1.4 1.4M5.3 18.7l1.4-1.4M17.3 6.7l1.4-1.4" />
          </>
        ) : (
          <path d="M19.5 14.5A8 8 0 0 1 9.5 4.5a8 8 0 1 0 10 10Z" />
        )}
      </Glyph>
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
  const label = n ? t("espace.archives.n", n) : t("espace.archives");
  const external = t("espace.external");
  return (
    <div className="hud-archives">
      <button type="button" className={`hud-icon${open ? " on" : ""}`} aria-expanded={open} aria-label={label} title={label} onClick={() => setOpen((o) => !o)}>
        <Glyph>
          <path d="M3.5 5h17v4h-17z" />
          <path d="M5 9v10h14V9" />
          <path d="M10 13h4" />
        </Glyph>
        {n > 0 && (
          <b className="hud-count" aria-hidden="true">
            {n > 99 ? "99+" : n}
          </b>
        )}
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

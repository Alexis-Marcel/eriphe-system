import { useEffect, useState, type ReactNode } from "react";
import { AnimatePresence, motion } from "motion/react";
import { stamp } from "../a2ui/format";
import { Settings } from "./Settings";
import { archives, reopen, type ArchivedNode } from "../data/api";
import { localURL } from "../data/presence";
import { dropNote, openPage, setTheme, step, store, useGraph } from "../data/store";
import { STREAM_GRACE } from "../data/stream";
import { NodeTitle } from "../graph/Title";
import { useWords } from "../i18n";

// The head-up display (#633), with no bar: the graph takes the whole
// screen, and its bubbles say what waits for the user and what runs. In
// the top-right corner, three icons: what the user archived, to reopen;
// the light or dark theme; the workspace's settings, and the way to the
// others (Settings.tsx). Under them, the notifications routed to this
// tab, and a word when the stream is down.

export function Hud({ live, narrow }: { live: boolean; narrow?: boolean }) {
  return (
    <>
      <div className="hud-corner">
        {live && <Archives />}
        <Theme />
        <Settings narrow={narrow} />
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
// The list is read once, then only when it may have changed: a node left
// the graph (archived), one was reopened, the panel opens.
function Archives() {
  const { t, lang } = useWords();
  const [open, setOpen] = useState(false);
  const [list, setList] = useState<ArchivedNode[] | null>(null);
  const [stale, setStale] = useState(0);
  const again = () => setStale((n) => n + 1);
  useEffect(() => store.subscribe((s, before) => s.nodes !== before.nodes && Object.keys(before.nodes).some((id) => !s.nodes[id]) && again()), []);
  useEffect(() => {
    let on = true;
    archives().then(
      (xs) => on && setList(xs),
      () => on && setList([]),
    );
    return () => {
      on = false;
    };
  }, [stale]);
  const toggle = () => {
    if (!open) again();
    setOpen(!open);
  };
  const back = (id: string) =>
    void reopen(id).then((ok) => {
      if (!ok) return;
      again();
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
      <button type="button" className={`hud-icon${open ? " on" : ""}`} aria-expanded={open} aria-label={label} title={label} onClick={toggle}>
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
                  const at = stamp(lang, x.archived);
                  return (
                    <li key={x.id}>
                      <span>
                        <b>
                          <NodeTitle x={x} fallback={external} />
                        </b>
                        <em>
                          {at ? t("espace.archives.at", at) : ""}
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
      {t("espace.offline")}
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

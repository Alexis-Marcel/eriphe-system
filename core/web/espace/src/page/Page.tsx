import { useEffect, useRef, useState, type PointerEvent as PtrEvent } from "react";
import { createPortal } from "react-dom";
import { stamp } from "../a2ui/format";
import { archive, label, type PageAlive, type PageOrigin, type TraceEntry } from "../data/api";
import { pathOf, subtree, useGraph } from "../data/store";
import { Doc } from "../doc/Doc";
import { titleOf } from "../graph/model";
import { NodeTitle, UnaskedMark } from "../graph/Title";
import { useWords } from "../i18n";
import { Cards } from "./Cards";
import { MdTab } from "./MdTab";
import { useFile, usePage, useTrace } from "./usePage";

// A node's page, drawn in the graph where its bubble was (#633): its head
// (what it belongs to, its title, its date or what is happening, its
// versions, the way to archive it, a small « ⋯ » menu for what is rarely
// wanted: its trace and its .md file, the button that closes it), its
// content, and at its foot the cards it waits on: under its head, the
// content and the cards scroll as one column, nothing cut off. In the
// graph it is moved by its head, like a bubble. Archiving is the user's
// alone, asked once more at the foot, with how many bubbles go with it.

type Tab = "doc" | "trace" | "md";

export type PageProps = {
  id: string;
  live: boolean;
  talking: boolean; // the console talks to it, and several pages are open
  onTalk: () => void;
  onClose: () => void;
  onGrab?: (e: PtrEvent) => void;
  onOpen: (id: string) => void;
};

export function Page({ id, live, talking, onTalk, onClose, onGrab, onOpen }: PageProps) {
  const { t, lang } = useWords();
  const nodes = useGraph((s) => s.nodes);
  const node = nodes[id];
  const [version, setVersion] = useState(0); // 0: the last
  const [tab, setTab] = useState<Tab>("doc");
  const [asking, setAsking] = useState(false); // whether to archive
  const { page, failed } = usePage(id, version, live);
  const trace = useTrace(id, tab === "trace", live);
  const file = useFile(id, version, tab === "md", live);
  const last = page?.versions.at(-1)?.n ?? node?.version ?? 1;
  const shown = page?.version ?? last;
  const latest = version === 0 || version === last;
  const external = t("espace.external");
  const path = pathOf(nodes, id);
  const kicker = path.length > 1 ? titleOf(nodes[path[0]], external) : null;
  const running = node?.state === "running";
  const at = stamp(lang, node?.updated);
  const shift = page?.shift ?? 0;
  const gone = (n: number) => (n === 0 ? "" : n === 1 ? t("espace.page.archive.one") : t("espace.page.archive.many", n));

  return (
    <article className={`pg${talking ? " talking" : ""}`} onPointerDown={onTalk} aria-label={titleOf(node, external)}>
      <header
        className="pg-head"
        onPointerDown={
          onGrab &&
          ((e) => {
            if ((e.target as HTMLElement).closest("button, input, a, select")) return;
            onTalk();
            onGrab(e);
          })
        }
      >
        {(kicker || page?.archived) && (
          <div className="pg-kicker">
            {kicker && path.slice(0, -1).map((p, i) => (
              <button key={p} type="button" className="pg-crumb" onClick={() => onOpen(p)}>
                {i > 0 && "› "}
                <NodeTitle x={nodes[p]} fallback={external} />
              </button>
            ))}
            {page?.archived && <em className="pg-badge">{t("espace.page.archived")}</em>}
          </div>
        )}
        <div className="pg-title">
          {/* Its title, then, on the same line, the small badge of a node
              someone else's words reached (its title marked when they
              may have written it), and the sign of a node the user did
              not ask for. */}
          <h2>
            <NodeTitle x={node} fallback={external} />
            {node?.external && (
              <em className="pg-badge warn" title={t("espace.external.why")}>
                {t("espace.external.mark")}
              </em>
            )}
            {node?.unasked && <UnaskedMark />}
          </h2>
          {node?.lifetime === "standing" && (
            <span className="pg-standing" role="img" aria-label={t("espace.standing")} title={t("espace.standing")}>
              ∞
            </span>
          )}
          <span className="pg-when">
            {running ? (
              <span className="pg-live">
                <i className="dot run" />
                {t("espace.page.running")}
              </span>
            ) : (
              at
            )}
          </span>
          {(page?.versions.length ?? 0) > 1 && (
            <span className="pg-versions" role="group" aria-label={t("espace.page.versions")}>
              {[...page!.versions].reverse().map((v) => (
                <button key={v.n} type="button" className={shown === v.n ? "on" : ""} aria-pressed={shown === v.n} onClick={() => setVersion(v.n === last ? 0 : v.n)}>
                  v{label(v.n, shift)}
                </button>
              ))}
            </span>
          )}
          {live && !page?.archived && (
            <button type="button" className="pg-archive" onClick={() => setAsking(true)}>
              {t("espace.page.archive")}
            </button>
          )}
          <More tab={tab} onTab={setTab} alive={page?.alive} origin={page?.origin} />
          <button type="button" className="pg-x" onClick={onClose} aria-label={t("espace.page.close")}>
            ✕
          </button>
        </div>
        {!latest && (
          <p className="pg-old">
            {t("espace.page.old", label(shown, shift))}{" "}
            <button type="button" className="linkish" onClick={() => setVersion(0)}>
              {t("espace.page.latest", label(last, shift))}
            </button>
          </p>
        )}
        {tab !== "doc" && (
          <p className="pg-view">
            <b>{t(`espace.page.${tab}`)}</b>
            <button type="button" className="linkish" onClick={() => setTab("doc")}>
              ← {t("espace.page.doc")}
            </button>
          </p>
        )}
      </header>
      {/* The body scrolls as one, the cards at its foot with it; focused,
          it takes the keyboard's arrows and pages. */}
      <div className="pg-body" tabIndex={0}>
        {tab === "doc" && (page ? <Doc page={page} latest={latest} live={live} onOpen={onOpen} /> : failed ? <p className="pg-empty">{t("espace.page.gone")}</p> : <Skeleton />)}
        {tab === "trace" && (trace ? <Trace entries={trace} /> : <Skeleton />)}
        {tab === "md" && (file ? <MdTab key={`${id}@${version}`} id={id} file={file} latest={latest} live={live} shift={shift} /> : <Skeleton />)}
        {page && page.cards.length > 0 && latest && (
          <footer className="pg-foot">
            <Cards surface={`${id}--cards`} refs={page.cards.map((c) => c.ref)} home={id} />
          </footer>
        )}
      </div>
      {asking && node && (
        <footer className="pg-foot pg-ask" role="alertdialog" aria-label={t("espace.page.archive.ask", titleOf(node, external))}>
          <p>
            <b>{t("espace.page.archive.ask", titleOf(node, external))}</b>
            <small>
              {gone(subtree(nodes, id).length - 1)} {t("espace.page.archive.back")}
            </small>
          </p>
          <span className="pg-ask-btns">
            <button type="button" className="btn" onClick={() => setAsking(false)}>
              {t("espace.page.archive.no")}
            </button>
            <button type="button" className="btn primary" onClick={() => void archive(id).finally(() => setAsking(false))}>
              {t("espace.page.archive")}
            </button>
          </span>
        </footer>
      )}
    </article>
  );
}

// More is the page's small menu: its trace and its .md file, rarely
// wanted, a click away; Escape or a click outside closes it. It is drawn
// over everything (the page clips what overflows it), under its button.
// It says where the node comes from (origin), and a permanent subject's
// what keeps it alive (alive).
function More({ tab, onTab, alive, origin }: { tab: Tab; onTab: (t: Tab) => void; alive?: PageAlive; origin?: PageOrigin }) {
  const { t } = useWords();
  const [open, setOpen] = useState<{ top: number; right: number } | null>(null);
  const box = useRef<HTMLSpanElement>(null);
  const list = useRef<HTMLSpanElement>(null);
  const toggle = () => {
    const r = box.current?.getBoundingClientRect();
    setOpen(open || !r ? null : { top: r.bottom + 4, right: innerWidth - r.right });
  };
  useEffect(() => {
    if (!open) return;
    const away = (e: Event) => !box.current?.contains(e.target as Node) && !list.current?.contains(e.target as Node) && setOpen(null);
    const esc = (e: KeyboardEvent) => e.key === "Escape" && setOpen(null);
    document.addEventListener("pointerdown", away);
    document.addEventListener("keydown", esc);
    return () => {
      document.removeEventListener("pointerdown", away);
      document.removeEventListener("keydown", esc);
    };
  }, [open]);
  const pick = (k: Tab) => {
    onTab(tab === k ? "doc" : k);
    setOpen(null);
  };
  return (
    <span className="pg-more" ref={box}>
      <button type="button" className="pg-more-btn" aria-haspopup="menu" aria-expanded={!!open} aria-label={t("espace.page.more")} title={t("espace.page.more")} onClick={toggle}>
        <svg viewBox="0 0 24 24" width="16" height="16" aria-hidden="true">
          <circle cx="5" cy="12" r="1.6" fill="currentColor" />
          <circle cx="12" cy="12" r="1.6" fill="currentColor" />
          <circle cx="19" cy="12" r="1.6" fill="currentColor" />
        </svg>
      </button>
      {open &&
        createPortal(
          <span className="pg-menu" role="menu" ref={list} style={{ top: open.top, right: open.right }}>
            {(["trace", "md"] as const).map((k) => (
              <button key={k} type="button" role="menuitemcheckbox" aria-checked={tab === k} onClick={() => pick(k)}>
                {t(`espace.page.${k}`)}
              </button>
            ))}
            {origin && <Origin origin={origin} />}
            {alive && <Alive alive={alive} />}
          </span>,
          document.body,
        )}
    </span>
  );
}

// Origin is who wanted the node, as eriphe-core recorded it when it made
// it: the user (their words quoted), a skill, an event, or not known.
export function Origin({ origin }: { origin: PageOrigin }) {
  const { t } = useWords();
  return (
    <section className={origin.unasked ? "pg-origin unasked" : "pg-origin"} role="group" aria-label={t("espace.origin")}>
      <h3>{t("espace.origin")}</h3>
      <p>
        {origin.unasked && <span aria-hidden="true">↯ </span>}
        {origin.text}
      </p>
    </section>
  );
}

// Alive is what keeps a permanent subject alive, as eriphe-core says it:
// its sources, monitors and skills, each by its name and what it does,
// and when it last changed.
export function Alive({ alive }: { alive: PageAlive }) {
  const { t, lang } = useWords();
  const groups = (["sources", "monitors", "skills"] as const).filter((k) => alive[k]?.length);
  return (
    <section className="pg-alive" role="group" aria-label={t("espace.alive")}>
      <h3>{t("espace.alive")}</h3>
      {groups.length === 0 && <p className="pg-alive-none">{t("espace.alive.none")}</p>}
      {groups.map((k) => (
        <div key={k} className={`pg-alive-${k}`}>
          <h4>{t(`espace.alive.${k}`)}</h4>
          <ul>
            {alive[k]!.map((l) => (
              <li key={l.name}>
                <b>{l.name}</b> <span>{l.what}</span>
              </li>
            ))}
          </ul>
        </div>
      ))}
      <p className="pg-alive-at">{t("espace.alive.updated", stamp(lang, alive.updated))}</p>
    </section>
  );
}

function Skeleton() {
  return (
    <div className="pg-skel" aria-busy="true">
      <i />
      <i />
      <i />
    </div>
  );
}

// Trace lists what was done for the node, oldest first: when, who, what.
function Trace({ entries }: { entries: TraceEntry[] }) {
  const { t, lang } = useWords();
  if (entries.length === 0) return <p className="pg-empty">{t("espace.page.trace.none")}</p>;
  return (
    <ol className="pg-trace">
      {entries.map((e, i) => {
        const who = e.author.startsWith("task:") ? "task" : e.author;
        return (
          <li key={i} className={`who-${who}`}>
            <time dateTime={e.at}>{stamp(lang, e.at)}</time>
            <span className="pg-who">{t(`espace.who.${["brain", "user", "task", "eriphe", "outside"].includes(who) ? who : "eriphe"}`)}</span>
            <p>{e.text}</p>
          </li>
        );
      })}
    </ol>
  );
}

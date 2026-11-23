import { useEffect, useRef, useState, type PointerEvent as PtrEvent } from "react";
import { createPortal } from "react-dom";
import { stamp } from "../a2ui/format";
import { archive, label, type PageAlive, type PageOrigin, type ProposalView, type TraceEntry } from "../data/api";
import { pathOf, subtree, useGraph } from "../data/store";
import { Doc } from "../doc/Doc";
import { titleOf } from "../graph/model";
import { ExtMark, NodeTitle, StandingMark, UnaskedMark } from "../graph/Title";
import { useWords } from "../i18n";
import { Cards } from "./Cards";
import { MdTab } from "./MdTab";
import { useFile, usePage, useTrace } from "./usePage";
import { Versions } from "./Versions";

// A node's page, drawn in the graph where its bubble was (#633): its head
// (what it belongs to, its title, its date or what is happening, its
// versions folded into one chip (Versions), the way to archive it, a small « ⋯ » menu for what is rarely
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
  // In the graph, like a window: big, enlarged to most of the screen; a
  // toggle (its button, a double click on its head) and a corner to drag
  // (or its arrows, from the keyboard) to give it a size. The phone's
  // list gives none: it takes the screen.
  big?: boolean;
  onBig?: () => void;
  onResize?: (e: PtrEvent) => void;
  onResizeBy?: (dw: number, dh: number) => void;
};

export function Page({ id, live, talking, onTalk, onClose, onGrab, onOpen, big, onBig, onResize, onResizeBy }: PageProps) {
  const { t, lang } = useWords();
  const nodes = useGraph((s) => s.nodes);
  const node = nodes[id];
  const [version, setVersion] = useState(0); // 0: the last
  const [tab, setTab] = useState<Tab>("doc");
  const [asking, setAsking] = useState(false); // whether to archive
  const { page, failed } = usePage(id, version);
  const trace = useTrace(id, tab === "trace");
  const file = useFile(id, version, tab === "md");
  const last = page?.versions.at(-1)?.n ?? node?.version ?? 1;
  const shown = page?.version ?? last;
  const latest = version === 0 || version === last;
  const external = t("espace.external");
  const path = pathOf(nodes, id);
  const kicker = path.length > 1 ? titleOf(nodes[path[0]], external) : null;
  const running = node?.status === "running";
  const at = stamp(lang, node?.updated);
  const shift = page?.shift ?? 0;
  // At its foot, only the cards that still ask something (eriphe-core's
  // cards: one that waits, or whose yes runs or failed). What is settled
  // leaves the page: it is in the « ⋯ » menu (Déjà décidé), and in the
  // trace.
  const waiting = page?.cards ?? [];
  const settled = page?.settled ?? [];
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
        onDoubleClick={onBig && ((e) => !(e.target as HTMLElement).closest("button, input, a, select") && onBig())}
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
          {/* Its title, on one line, cut short (whole in its tooltip) so
              that nothing beside it pushes it onto two; then, on the same
              line, the one ⚠ of a node someone else's words reached (its
              title among them, or its content: its words say which), and
              the sign of a node the user did not ask for. That ⚠ is the
              page's one sign for it: inside, a block only has its quiet
              edge (Doc.tsx). */}
          <h2>
            <span className="pg-title-tx" title={node?.title || external}>
              <NodeTitle x={node} fallback={external} mark={false} />
            </span>
            {(node?.external || node?.titleExternal || page?.parts.some((x) => x.block?.external)) && (
              <ExtMark why={node?.external || page?.parts.some((x) => x.block?.external) ? t("espace.external.why") : undefined} />
            )}
            {node?.unasked && <UnaskedMark />}
          </h2>
          {node?.lifetime === "standing" && <StandingMark />}
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
          {page && <Versions versions={page.versions} shown={shown} last={last} shift={shift} onPick={(n) => setVersion(n === last ? 0 : n)} />}
          {live && !page?.archived && (
            <button type="button" className="pg-archive" onClick={() => setAsking(true)}>
              {t("espace.page.archive")}
            </button>
          )}
          <More tab={tab} onTab={setTab} alive={page?.alive} origin={page?.origin} settled={settled} />
          {onBig && (
            <button type="button" className="pg-big" aria-pressed={!!big} onClick={onBig} title={t(big ? "espace.page.small" : "espace.page.big")} aria-label={t(big ? "espace.page.small" : "espace.page.big")}>
              <svg viewBox="0 0 16 16" width="13" height="13" aria-hidden="true">
                <path d={big ? "M6.5 2.5v4h-4M9.5 13.5v-4h4" : "M2.5 6.5v-4h4M13.5 9.5v4h-4"} fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round" />
              </svg>
            </button>
          )}
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
        {waiting.length > 0 && latest && (
          <footer className="pg-foot">
            <Cards surface={`${id}--cards`} refs={waiting.map((c) => c.ref)} home={id} />
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
      {onResize && (
        <button
          type="button"
          className="pg-resize"
          aria-label={t("espace.page.resize")}
          title={t("espace.page.resize")}
          onPointerDown={(e) => {
            onTalk();
            onResize(e);
          }}
          onKeyDown={(e) => {
            const step = e.shiftKey ? 120 : 40;
            const d = { ArrowLeft: [-step, 0], ArrowRight: [step, 0], ArrowUp: [0, -step], ArrowDown: [0, step] }[e.key];
            if (!d || !onResizeBy) return;
            e.preventDefault();
            onResizeBy(d[0], d[1]);
          }}
        >
          <svg viewBox="0 0 12 12" width="12" height="12" aria-hidden="true">
            <path d="M11 4 4 11M11 8 8 11" fill="none" stroke="currentColor" strokeWidth="1.3" strokeLinecap="round" />
          </svg>
        </button>
      )}
    </article>
  );
}

// More is the page's small menu: its trace and its .md file, rarely
// wanted, a click away; Escape or a click outside closes it. It is drawn
// over everything (the page clips what overflows it), under its button.
// It says where the node comes from (origin), and a permanent subject's
// what keeps it alive (alive).
function More({ tab, onTab, alive, origin, settled = [] }: { tab: Tab; onTab: (t: Tab) => void; alive?: PageAlive; origin?: PageOrigin; settled?: ProposalView[] }) {
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
            {settled.length > 0 && <Settled cards={settled} />}
            {origin && <Origin origin={origin} />}
            {alive && <Alive alive={alive} />}
          </span>,
          document.body,
        )}
    </span>
  );
}

// outcomeOf is how a decided card went: done, failed, declined or
// withdrawn.
export function outcomeOf(c: ProposalView): "done" | "failed" | "declined" | "withdrawn" {
  if (c.state === "approved") return c.exec === "failed" ? "failed" : "done";
  return c.state === "rejected" ? "declined" : "withdrawn";
}

// Settled are the cards of the node already decided, the last first: a
// line each, what it was and how it went; a failed one says why, the
// tool's own words marked when they may be someone else's.
export function Settled({ cards }: { cards: ProposalView[] }) {
  const { t } = useWords();
  return (
    <section className="pg-settled" role="group" aria-label={t("espace.page.settled")}>
      <h3>{t("espace.page.settled")}</h3>
      <ul>
        {cards.map((c) => {
          const o = outcomeOf(c);
          const why = o === "failed" ? c.execError || (c.execWhy ? t(`espace.a2.proposal.why.${c.execWhy}`) : "") : "";
          return (
            <li key={c.ref} className={o === "done" ? "yes" : o === "failed" ? "failed" : "no"}>
              <span className="pg-settled-word">{t(`espace.page.settled.${o}`)}</span>
              <span className="pg-settled-text">{c.text}</span>
              {why && (
                <small className="pg-settled-why">
                  {why}
                  {c.execError && c.execErrorExternal && <ExtMark why={t("espace.external.why")} />}
                </small>
              )}
            </li>
          );
        })}
      </ul>
    </section>
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
      {/* What the user approved the view for: its notes, once, here. */}
      {alive.decided && (
        <div className="pg-alive-decided">
          <h4>{t("espace.page.decided")}</h4>
          <p>
            {alive.decided}
            {alive.decidedExternal && <ExtMark why={t("espace.external.why")} />}
          </p>
        </div>
      )}
      {groups.length === 0 && !alive.decided && <p className="pg-alive-none">{t("espace.alive.none")}</p>}
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

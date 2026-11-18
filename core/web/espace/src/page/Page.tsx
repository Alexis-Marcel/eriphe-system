import { useEffect, useMemo, useState, useSyncExternalStore, type PointerEvent as PtrEvent } from "react";
import { createAdapter } from "../a2ui/adapter";
import { CATALOG } from "../a2ui/catalog";
import { wall, when } from "../a2ui/format";
import { SurfaceView } from "../a2ui/Surfaces";
import { archive, label, type ProposalView, type TraceEntry } from "../data/api";
import { pathOf, subtree, useGraph } from "../data/store";
import { Doc } from "../doc/Doc";
import { titleOf } from "../graph/model";
import { useWords } from "../i18n";
import { MdTab } from "./MdTab";
import { useFile, usePage, useTrace } from "./usePage";

// A node's page, drawn in the graph where its bubble was (#633): its head
// (what it belongs to, its title, its date or what is happening, its
// versions, the way to archive it, the button that closes it), the tabs
// Content, Trace and .md, and at its foot the cards it waits on. In the
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
  const at = wall(node?.updated);
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
        {(kicker || node?.external || page?.archived) && (
          <div className="pg-kicker">
            {kicker && path.slice(0, -1).map((p, i) => (
              <button key={p} type="button" className="pg-crumb" onClick={() => onOpen(p)}>
                {i > 0 && "› "}
                {titleOf(nodes[p], external)}
              </button>
            ))}
            {node?.external && <em className="pg-badge warn">{t("espace.external.mark")}</em>}
            {page?.archived && <em className="pg-badge">{t("espace.page.archived")}</em>}
          </div>
        )}
        <div className="pg-title">
          <h2>{titleOf(node, external)}</h2>
          <span className="pg-when">
            {running ? (
              <span className="pg-live">
                <i className="dot run" />
                {t("espace.page.running")}
              </span>
            ) : at ? (
              when(lang, at)
            ) : null}
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
        <nav className="pg-tabs" role="tablist">
          {(["doc", "trace", "md"] as const).map((k) => (
            <button key={k} type="button" role="tab" aria-selected={tab === k} onClick={() => setTab(k)}>
              {t(`espace.page.${k}`)}
            </button>
          ))}
        </nav>
      </header>
      <div className="pg-body">
        {tab === "doc" && (page ? <Doc page={page} latest={latest} live={live} /> : failed ? <p className="pg-empty">{t("espace.page.gone")}</p> : <Skeleton />)}
        {tab === "trace" && (trace ? <Trace entries={trace} /> : <Skeleton />)}
        {tab === "md" && (file ? <MdTab key={`${id}@${version}`} id={id} file={file} latest={latest} live={live} shift={shift} /> : <Skeleton />)}
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
      {page && page.cards.length > 0 && latest && (
        <footer className="pg-foot">
          <Cards id={id} cards={page.cards} />
        </footer>
      )}
    </article>
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
        const w = wall(e.at);
        const who = e.author.startsWith("task:") ? "task" : e.author;
        return (
          <li key={i} className={`who-${who}`}>
            <time dateTime={e.at}>{w ? when(lang, w) : ""}</time>
            <span className="pg-who">{t(`espace.who.${["brain", "user", "task", "eriphe", "outside"].includes(who) ? who : "eriphe"}`)}</span>
            <p>{e.text}</p>
          </li>
        );
      })}
    </ol>
  );
}

// Cards are the cards a node waits on, as the catalog's Proposal shows
// them: code's own surface, each answered by its card's Oui or Non.
function Cards({ id, cards }: { id: string; cards: ProposalView[] }) {
  const { lang } = useWords();
  const [adapter] = useState(() => createAdapter(lang));
  useEffect(() => () => adapter.dispose(), [adapter]);
  const refs = useMemo(() => cards.map((c) => c.ref), [cards]);
  const surface = `${id}--cards`;
  useEffect(() => {
    adapter.applyMessages(adapter.surfaces().map((s) => ({ version: "1.0" as const, deleteSurface: { surfaceId: s.id } })));
    adapter.applyMessages([
      {
        version: "1.0",
        createSurface: {
          surfaceId: surface,
          catalogId: CATALOG.id,
          components: [{ id: "root", component: "Column", children: refs.map((_, i) => `c${i}`) }, ...refs.map((ref, i) => ({ id: `c${i}`, component: "Proposal", proposalId: ref }))],
          dataModel: {},
          metadata: { extensions: { eriphe: { author: "core" } } },
        },
      },
    ]);
  }, [adapter, refs, surface]);
  const s = useSyncExternalStore(adapter.subscribe, adapter.surfaces).find((x) => x.id === surface);
  return s ? <SurfaceView surface={s} /> : null;
}

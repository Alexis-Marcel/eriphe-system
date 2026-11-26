import { useEffect, useMemo, useState, type ReactNode } from "react";
import { Icon } from "../a2ui/icons";
import { InlineYes } from "../a2ui/inline";
import { actYes, composeAct, type ActItem, type NodePage, type PageAct } from "../data/api";
import { useWords } from "../i18n";
import { readPage, type MdItem, type PageLink } from "./read";

// An action on a heading's items (do:, page_acts.go): the page says where
// it is, eriphe-core composes it from the page as stored (the references
// under the heading to its tool's server's items, the ticked ones left
// out, each as its server read it) and says it in its own words, never the
// link's. Its line carries its Oui; each row it acts on carries a box,
// ticked: the user unticks what stays. Oui sends the ids ticked with the
// line as the page read it; eriphe-core reads the page again and runs
// only those still under the heading, as one card said yes to, whose
// state the line then shows (running, done). Nothing is kept before the
// yes: an older version, or a do: that acts on nothing, is its words,
// quiet (why in its tooltip).

export type PageActs = {
  // link draws a do: on its line; undefined: as its words.
  link: (l: PageLink) => ReactNode | undefined;
  // item draws the boxes of a row a do: acts on.
  item: (it: MdItem) => ReactNode;
  // item an act names on line ln by its reference's label: its words as
  // its server read them (a warning when the page's do not say them).
  ref: (label: string, ln: number) => ActItem | undefined;
};

export function usePageActs(page: NodePage, latest: boolean): PageActs {
  const id = page.node.id;
  const body = page.body ?? "";
  const links = useMemo(() => (latest ? readPage(body).links.filter((l) => l.kind === "do") : []), [body, latest]);
  const [acts, setActs] = useState<Map<number, PageAct>>(() => new Map());
  const [left, setLeft] = useState<Map<number, Set<string>>>(() => new Map());
  const [done, setDone] = useState<Map<number, string>>(() => new Map());
  // Composed again for each version of the page (its items read by their
  // server), not at each of its moves.
  const version = page.version;
  useEffect(() => {
    if (!links.length) {
      setActs(new Map());
      return;
    }
    let live = true;
    const lines = body.replace(/\r\n/g, "\n").split("\n");
    void Promise.all(links.map((l) => composeAct(id, l.line, lines[l.line - 1] ?? "").catch(() => null))).then((xs) => {
      if (!live) return;
      const m = new Map<number, PageAct>();
      xs.forEach((a, i) => a && m.set(links[i].line, a));
      setActs(m);
    });
    return () => {
      live = false;
    };
  }, [id, body, version, links]);
  // What a yes did shows until the page's text changes (its skill wrote
  // it again).
  useEffect(() => setDone(new Map()), [id, body]);

  return useMemo<PageActs>(() => {
    const toggle = (ln: number, ids: string[], on: boolean) =>
      setLeft((m) => {
        const s = new Set(m.get(ln));
        for (const x of ids) if (on) s.delete(x);
        else s.add(x);
        return new Map(m).set(ln, s);
      });
    const ran = (ln: number, card: string, ids: string[]) => {
      setDone((m) => new Map(m).set(ln, card));
      setActs((m) => {
        const a = m.get(ln);
        return a ? new Map(m).set(ln, { ...a, items: a.items.filter((it) => !ids.includes(it.id)) }) : m;
      });
    };
    return {
      link: (l) => {
        if (l.kind !== "do") return undefined;
        const a = acts.get(l.line);
        if (!a) return undefined;
        return <DoLine id={id} l={l} a={a} left={left.get(l.line)} card={done.get(l.line)} ran={ran} />;
      },
      item: (it) => {
        const out: ReactNode[] = [];
        for (const [ln, a] of acts) {
          const mine = a.items.filter((x) => x.row === it.line);
          if (!mine.length || a.refused) continue;
          const ids = mine.map((x) => x.id);
          const off = left.get(ln);
          const on = ids.some((x) => !off?.has(x));
          out.push(<DoTick key={ln} ln={ln} items={mine} on={on} toggle={(v) => toggle(ln, ids, v)} />);
        }
        return out.length ? <>{out}</> : null;
      },
      ref: (label, ln) => {
        for (const a of acts.values()) {
          const x = a.items.find((it) => it.line === ln && it.label === label);
          if (x) return x;
        }
        return undefined;
      },
    };
  }, [id, acts, left, done]);
}

// DoTick is a row's box for a do: of its heading: ticked, it goes with
// the Oui; unticked, it stays.
function DoTick({ ln, items, on, toggle }: { ln: number; items: ActItem[]; on: boolean; toggle: (on: boolean) => void }) {
  const { t } = useWords();
  const name = items.map((x) => x.title || x.label).join(", ");
  return (
    <input
      type="checkbox"
      className="a2-tick md-do-tick"
      data-do={ln}
      checked={on}
      onChange={(e) => toggle(e.target.checked)}
      onClick={(e) => e.stopPropagation()}
      aria-label={t("espace.batch.keep", name)}
      title={t("espace.batch.keep", name)}
    />
  );
}

// DoLine is a do: on its line: what Oui does to the items ticked, in
// code's words, and its Oui; once said yes, its card's state.
function DoLine({ id, l, a, left, card, ran }: { id: string; l: PageLink; a: PageAct; left?: Set<string>; card?: string; ran: (ln: number, card: string, ids: string[]) => void }) {
  const { t } = useWords();
  const [busy, setBusy] = useState(false);
  const [failed, setFailed] = useState("");
  const quiet = (why: string) => (
    <span className={`md-link md-link-do md-do-quiet`} data-line={l.line} title={why}>
      {l.label}
    </span>
  );
  const said = card && <InlineYes refText={card} where={{ surfaceId: `${id}--page`, componentId: `do-${l.line}` }} />;
  if (said && (a.refused || !a.items.length))
    return (
      <span className="md-do" data-line={l.line}>
        {said}
      </span>
    );
  if (a.refused) return quiet(a.why ?? "");
  if (!a.items.length) return quiet(t("espace.md.do.none"));
  const going = a.items.filter((x) => !left?.has(x.id));
  const n = going.length;
  const kept = a.items.length - n;
  const say = n ? (a.acts?.[n - 1] ?? "") : t("espace.md.do.nothing");
  const verb = n ? (a.verbs?.[n - 1] ?? "") : (a.verbs?.[0] ?? "");
  const yes = async () => {
    if (busy || !n) return;
    setBusy(true);
    setFailed("");
    const ids = going.map((x) => x.id);
    const r = await actYes(id, a.line, a.text, ids).catch(() => ({ error: t("espace.a2.proposal.failed") }) as { card?: string; error?: string });
    setBusy(false);
    if (r.card) ran(l.line, r.card, ids);
    else setFailed(r.error || t("espace.a2.proposal.failed"));
  };
  return (
    <span className="a2-inline md-do" data-line={l.line} aria-label={say}>
      {said}
      {a.tainted && (
        <span className="a2-inline-ext" title={t("espace.a2.proposal.tainted")}>
          <Icon name="warning" />
          <span className="sr-only">{t("espace.a2.proposal.tainted")}</span>
        </span>
      )}
      <span className="a2-inline-what" title={t("espace.md.do.what")}>
        {say}
      </span>
      {kept > 0 && <span className="md-do-kept">{t("espace.batch.kept", kept)}</span>}
      <button type="button" className={`btn a2-button a2-button-primary a2-inline-yes${busy ? " is-busy" : ""}`} disabled={busy || !n} title={say} aria-busy={busy} onClick={yes}>
        {verb}
      </button>
      {failed && (
        <span className="a2-proposal-failed" role="alert">
          {failed}
        </span>
      )}
    </span>
  );
}

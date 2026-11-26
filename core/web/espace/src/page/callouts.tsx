import { useState, type ReactNode } from "react";
import { Agenda as AgendaView, AGENDA_DAYS, addDays, type Listed } from "../a2ui/agenda";
import { BlockTitle, FiguresView } from "../a2ui/blocks";
import { AlertView, factLink, PersonView } from "../a2ui/catalog";
import { Chart as ChartView, type Series } from "../a2ui/chart";
import { Icon } from "../a2ui/icons";
import { useNow } from "../a2ui/now";
import { useWords } from "../i18n";
import { Blocks, Lines, Pieces, useMd } from "./Body";
import { dayOf, figure, frNumber, pieces, plain, split } from "./line";
import type { MdBlock, MdItem } from "./read";

// A page's callouts (> [!type] Title), each drawn by the catalog's view of
// its kind: [!figures] Figures, [!chart] Chart, [!steps] a Timeline whose
// steps fold their detail, [!agenda] Agenda, [!person] Person, [!warning],
// [!info] and [!success] an Alert; [!note], and a type Eriphe does not
// draw (eriphe-core's check tells its author), a quiet box of its blocks.
// What a typed callout does not read of its body is drawn under it, never
// dropped. Folded (> [!type]- Title), it opens on a click; + open.

export function Callout({ b }: { b: MdBlock }) {
  const inner = <Typed b={b} />;
  if (!b.fold) return <div className={`md-callout md-callout-${b.type}`}>{inner}</div>;
  return (
    <Fold title={b.title ? <Lines text={b.title} ln={b.line} /> : b.type!} start={b.fold === "+"} type={b.type!}>
      {inner}
    </Fold>
  );
}

function Fold({ title, start, type, children }: { title: ReactNode; start: boolean; type: string; children: ReactNode }) {
  const [open, setOpen] = useState(start);
  return (
    <div className={`a2-sec-fold md-fold md-callout-${type}${open ? " open" : ""}`}>
      <div className="a2-sec-fold-line">
        <button type="button" className="a2-sec-fold-head" aria-expanded={open} onClick={() => setOpen(!open)}>
          <span className="a2-sec-chev" aria-hidden="true" />
          <span className="a2-sec-fold-name">{title}</span>
        </button>
      </div>
      {open && <div className="a2-sec-fold-body">{children}</div>}
    </div>
  );
}

// What each typed callout reads of its body.
const READS: Record<string, MdBlock["kind"][]> = { figures: ["list"], chart: ["table"], steps: ["list"], person: ["list", "paragraph"] };
const LEVELS: Record<string, "warning" | "info" | "success"> = { warning: "warning", info: "info", success: "success" };

const itemsOf = (bs: MdBlock[]) => bs.flatMap((x) => (x.kind === "list" ? x.items! : []));

function Rest({ blocks }: { blocks: MdBlock[] }) {
  if (!blocks.length) return null;
  return (
    <div className="md-leftover">
      <Blocks blocks={blocks} />
    </div>
  );
}

function Typed({ b }: { b: MdBlock }) {
  const body = b.body ?? [];
  const title = b.title ? plain(b.title) : "";
  const reads = READS[b.type!];
  const rest = reads ? body.filter((x) => !reads.includes(x.kind)) : [];
  switch (b.type) {
    case "figures":
      return (
        <>
          <Figures title={title} items={itemsOf(body)} />
          <Rest blocks={rest} />
        </>
      );
    case "chart":
      return <Chart b={b} title={title} />;
    case "steps":
      return (
        <>
          <Steps title={title} items={itemsOf(body)} />
          <Rest blocks={rest} />
        </>
      );
    case "agenda":
      return <Agenda title={title} body={body} />;
    case "person":
      return (
        <>
          <Person name={title} body={body} />
          <Rest blocks={rest} />
        </>
      );
  }
  const level = LEVELS[b.type!];
  if (level) {
    // Its paragraphs are its text; its other blocks (a list, its ask:
    // line) follow in it.
    const text = body.filter((x) => x.kind === "paragraph");
    const others = body.filter((x) => x.kind !== "paragraph");
    return (
      <AlertView
        level={level}
        title={b.title ? <Lines text={b.title} ln={b.line} /> : undefined}
        text={text.length ? text.map((x, i) => (
          <span key={x.line}>
            {i > 0 && " "}
            <Lines text={x.text!} ln={x.line} />
          </span>
        )) : undefined}
      >
        {others.length > 0 && (
          <div className="md-alert-rest">
            <Blocks blocks={others} />
          </div>
        )}
      </AlertView>
    );
  }
  // A note, or a type Eriphe does not draw: its blocks in a quiet box.
  return (
    <div className={`md-box md-box-${b.type}`}>
      {b.title && !b.fold && (
        <p className="md-box-title">
          <Lines text={b.title} ln={b.line} />
        </p>
      )}
      <Blocks blocks={body} />
    </div>
  );
}

// ---- Figures ----

function Figures({ title, items }: { title: string; items: MdItem[] }) {
  return (
    <FiguresView
      title={title}
      items={items.map((it) => {
        const f = figure(it.text);
        return {
          label: <Pieces ps={f.label} ln={it.line} />,
          value: f.amount ? f.amount.n : <Pieces ps={f.value} ln={it.line} />,
          unit: f.amount?.unit,
          delta: f.delta,
          deltaUnit: f.deltaUnit,
          deltaLabel: f.deltaLabel,
        };
      })}
    />
  );
}

// ---- Chart: a table, its first column the x, a column per series, the
// unit in the header's brackets (« Dépenses (€) »). ----

function Chart({ b, title }: { b: MdBlock; title: string }) {
  const { t, lang } = useWords();
  const body = b.body ?? [];
  const at = body.findIndex((x) => x.kind === "table");
  const rest = body.filter((_, i) => i !== at);
  if (at < 0)
    return (
      <>
        <BlockTitle text={title} />
        <p className="a2-empty">{t("espace.a2.chart.empty")}</p>
        <Rest blocks={rest} />
      </>
    );
  const tb = body[at];
  const kind = (b.opts ?? []).find((o): o is "line" | "bar" | "stack" => o === "line" || o === "bar" || o === "stack") ?? "line";
  const heads = tb.head!.map((h) => /^(.*?)\s*\(([^)]+)\)\s*$/.exec(plain(h)));
  const unit = heads.slice(1).find(Boolean)?.[2] ?? /\(([^)]+)\)\s*$/.exec(title)?.[1];
  const series: Series[] = tb.head!.slice(1).map((h, c) => ({
    name: heads[c + 1]?.[1] ?? plain(h),
    points: (tb.rows ?? []).flatMap((r) => {
      const y = frNumber(r[c + 1] ?? "");
      return y === undefined ? [] : [{ x: plain(r[0]), y }];
    }),
  }));
  const names = series.map((s) => s.name).filter(Boolean).join(", ");
  // A cell that is no number is not drawn: the table is, under it.
  const unread = (tb.rows ?? []).some((r) => r.slice(1).some((c) => c !== "" && frNumber(c) === undefined));
  return (
    <>
      <BlockTitle text={title.replace(/\s*\([^)]*\)\s*$/, "")} />
      <ChartView kind={kind} series={series} unit={unit} lang={lang} label={t(`espace.a2.chart.${kind}`, names || "—")} />
      <Rest blocks={unread ? body : rest} />
    </>
  );
}

// ---- Steps: the catalog's Timeline, each step's detail folded under it ----

const stepState = (it: MdItem) => (it.task === "x" ? "done" : it.task === "/" ? "current" : it.task === "-" ? "failed" : "pending");

function Steps({ title, items }: { title: string; items: MdItem[] }) {
  const { t } = useWords();
  const done = items.filter((it) => it.task === "x").length;
  const current = items.find((it) => it.task === "/");
  const left = items.some((it) => it.task !== "x" && it.task !== "-");
  return (
    <div className="md-steps">
      <BlockTitle text={title} />
      {left && items.length > 1 && (
        <div className="a2-tl-prog">
          <p className="a2-tl-prog-words">
            <b>{t("espace.a2.progress.of", done, items.length)}</b>
            {current && <span> · {plain(current.text)}</span>}
          </p>
          <span className="a2-bar" role="img" aria-label={t("espace.a2.progress", done, items.length)}>
            {items.map((it, i) => (
              <span key={i} className={it.task === "x" ? "a2-seg-done" : it.task === "/" ? "a2-seg-now" : "a2-seg"} />
            ))}
          </span>
        </div>
      )}
      <ol className="a2-timeline">
        {items.map((it) => (
          <Step key={it.line} it={it} />
        ))}
      </ol>
    </div>
  );
}

function Step({ it }: { it: MdItem }) {
  const { t } = useWords();
  const [open, setOpen] = useState(false);
  const st = stepState(it);
  const ps = pieces(it.text);
  const due = ps.filter((p) => p.t === "due");
  const words = ps.filter((p) => p.t !== "due");
  return (
    <li className={`a2-step a2-step-${st}${it.task === "-" ? " md-dropped" : ""}`} aria-current={st === "current" ? "step" : undefined} data-line={it.line}>
      <span className="a2-step-dot" aria-hidden="true">
        {st === "done" ? <Icon name="check" /> : st === "failed" ? <Icon name="close" /> : null}
      </span>
      <span className="a2-step-body">
        {it.detail ? (
          <button type="button" className="a2-step-label md-step-open" aria-expanded={open} onClick={() => setOpen(!open)}>
            <Pieces ps={words} ln={it.line} />
            <span className="a2-sec-chev md-chev-s" aria-hidden="true" />
          </button>
        ) : (
          <span className="a2-step-label">
            <Pieces ps={words} ln={it.line} />
          </span>
        )}
        <span className="sr-only">, {t(`espace.a2.step.${st}`)}</span>
        {due.length > 0 && (
          <span className="a2-step-date">
            {" "}
            <Pieces ps={due} ln={it.line} />
          </span>
        )}
        {open && it.detail && (
          <span className="a2-step-detail md-step-detail">
            <Blocks blocks={it.detail} />
          </span>
        )}
      </span>
    </li>
  );
}

// ---- Agenda: « ### Jeudi 26 novembre » a day, « - 09:00–10:00 Titre · lieu »
// an event; its title a reference to its event, its detail the lines under
// it. A day that reads as none, or past the days an Agenda draws, is drawn
// as written under it. ----

const EVENT = /^(\d{1,2})[:h](\d{2})(?:\s*[–-]\s*(\d{1,2})[:h](\d{2}))?\s+(.*)$/;

function Agenda({ title, body }: { title: string; body: MdBlock[] }) {
  const ctx = useMd();
  const { t, lang } = useWords();
  const now = useNow();
  const days = body.flatMap((x) => (x.kind === "heading" ? [dayOf(x.text!, ctx.year)] : [])).filter((d): d is string => !!d).sort();
  const first = days[0];
  const last = first ? addDays(first, AGENDA_DAYS - 1) : "";
  const events: Listed[] = [];
  const rest: MdBlock[] = [];
  let day: string | undefined;
  for (const x of body) {
    if (x.kind === "heading") {
      const d = dayOf(x.text!, ctx.year);
      day = d && d <= last ? d : undefined;
      if (!day) rest.push(x);
      continue;
    }
    if (x.kind !== "list" || !day) {
      rest.push(x);
      continue;
    }
    for (const it of x.items!) {
      const m = EVENT.exec(it.text.trim());
      const words = m ? m[5] : it.text;
      // « Titre · lieu »: the place after the last « · ».
      const cut = words.lastIndexOf(" · ");
      const head = cut >= 0 ? words.slice(0, cut) : words;
      const place = cut >= 0 ? plain(words.slice(cut + 3)) : "";
      const pad = (s: string) => s.padStart(2, "0");
      const ps = pieces(head);
      events.push({
        n: events.length,
        title: plain(ps),
        start: m ? `${day}T${pad(m[1])}:${m[2]}:00` : day,
        ...(m?.[3] ? { end: `${day}T${pad(m[3])}:${m[4]}:00` } : {}),
        ...(m ? {} : { allDay: true }),
        ...(place ? { place } : {}),
        ...(ps.some((p) => p.t !== "text") || it.detail ? { label: <Pieces ps={ps} ln={it.line} /> } : {}),
        ...(it.detail ? { detail: <Blocks blocks={it.detail} /> } : {}),
      });
    }
  }
  const span = first ? Math.round((Date.parse(days[days.length - 1]) - Date.parse(first)) / 86400000) + 1 : 0;
  return (
    <>
      {first && (
        <>
          <BlockTitle text={title} />
          <AgendaView date={first} days={Math.min(AGENDA_DAYS, span)} events={events} ghosts={[]} lang={lang} t={t} now={now} />
        </>
      )}
      {!first && title && <BlockTitle text={title} />}
      {first ? <Rest blocks={rest} /> : <Blocks blocks={rest} />}
    </>
  );
}

// ---- Person: « - Rôle : … » what they are to the user, « - Téléphone :
// … » a fact each, its paragraph a sentence of where things stand. ----

function Person({ name, body }: { name: string; body: MdBlock[] }) {
  let detail: ReactNode;
  const facts: { label: ReactNode; value: ReactNode; href?: string | null }[] = [];
  for (const it of itemsOf(body)) {
    const { title, comment } = split(pieces(it.text));
    const label = plain(title);
    const value = <Pieces ps={comment} ln={it.line} />;
    if (/^(r[ôo]le|role)$/i.test(label)) detail = value;
    else facts.push({ label, value, href: factLink(plain(comment)) });
  }
  const notes = body.filter((x) => x.kind === "paragraph");
  const note = notes.length ? notes.map((x, i) => (
    <span key={x.line}>
      {i > 0 && " "}
      <Lines text={x.text!} ln={x.line} />
    </span>
  )) : undefined;
  return <PersonView name={name} detail={detail} note={note} facts={facts} />;
}

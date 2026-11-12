import type { ReactNode } from "react";
import { overlapping, type Size } from "./engine";
import { clock, dayLabel, shortDate, useWords, type T } from "./i18n";
import type { Obj, Ref } from "./types";

// One view per kind of object (objects.go), never per situation. Each has a
// short form (size s) and a full form (large sizes, or the focus). A kind
// without a view of its own shows its title.

export type Act = {
  open: (ref: Ref) => void;
  answer: (ref: Ref, yes: boolean) => void;
  ask: (text: string) => void;
};

const nameOf = (from: string) => from.split(" <")[0].replace(/^"|"$/g, "");

// A step's status, as a session's timeline draws it (sessions.go).
const stepClass = (status: string) =>
  ["reported", "verified", "accepted"].includes(status) ? "done" : ["rejected", "blocked"].includes(status) ? "failed" : status;

export function View({ o, size, full, all, act }: { o: Obj; size: Size; full: boolean; all: Obj[]; act: Act }) {
  const { t, lang } = useWords();
  const short = size === "s" && !full;
  const big = full || size === "xl" || size === "l";
  const m = o.mail, a = o.agenda, e = o.event, s = o.session, c = o.card, k = o.task, p = o.project;

  if (m) {
    const files = m.attachments?.length ?? 0;
    return (
      <>
        <p className="mod-kicker">{nameOf(m.from)}, {clock(lang, m.date)}</p>
        <h2 className="mod-title">{m.subject || o.title}</h2>
        {!short && <p className={`mod-text ${full ? "mail-body" : "excerpt"}`}>{full ? m.body : m.body.slice(0, 400)}</p>}
        {!short && files > 0 && <p className="mod-meta">{t("espace.mail.attachments", files)}</p>}
        {!short && (
          <div className="actions">
            {!full && <button className="btn primary" onClick={() => act.open(o.ref)}>{t("espace.mail.open")}</button>}
            <button className={`btn ${full ? "primary" : ""}`} onClick={() => act.ask(t("espace.reply.message", titleOf(t, lang, o)))}>
              {t("espace.mail.reply")}
            </button>
          </div>
        )}
      </>
    );
  }

  if (a) {
    const clash = overlapping(a.events);
    return (
      <>
        <p className="mod-kicker">{dayLabel(lang, a.date)}</p>
        {a.events.length === 0 && <p className="mod-meta">{t("espace.agenda.none")}</p>}
        {short && a.events.length > 0 && <p className="mod-title">{t("espace.agenda.count", a.events.length)}</p>}
        {!short && (
          <ol className="events">
            {a.events.map((ev, i) => (
              <li key={ev.id || ev.start + ev.title} className={`ev ${clash.has(i) ? "conflict" : ""}`}>
                <span className="ev-time">{ev.all_day ? t("espace.allday") : `${clock(lang, ev.start)}–${clock(lang, ev.end)}`}</span>
                <span className="ev-title">{ev.title}{ev.location && big ? `, ${ev.location}` : ""}</span>
                {clash.has(i) && <span className="ev-warn">{t("espace.ev.overlap")}</span>}
              </li>
            ))}
          </ol>
        )}
      </>
    );
  }

  if (e) {
    return (
      <>
        <p className="mod-kicker">{t("espace.event")}, {shortDate(lang, e.start)}</p>
        <h2 className="mod-title">{e.title}</h2>
        <p className="mod-meta">{e.all_day ? t("espace.allday") : `${clock(lang, e.start)}–${clock(lang, e.end)}`}{e.location && !short ? `, ${e.location}` : ""}</p>
        {full && e.description && <p className="mod-text">{e.description}</p>}
      </>
    );
  }

  if (c) {
    return (
      <>
        <p className="mod-kicker">{c.state === "pending" ? t("espace.card.kicker", clock(lang, c.created)) : t(`espace.card.${c.state}`)}</p>
        <h2 className="mod-title">{c.text}</h2>
        {!short && c.why && <p className="mod-text">{c.why}</p>}
        {big && c.fields && c.fields.length > 0 && (
          <dl className="fields">
            {c.fields.map((f) => <div key={f.name}><dt>{f.name}</dt><dd>{f.value}</dd></div>)}
          </dl>
        )}
        {!short && c.state === "pending" && <YesNo ref_={o.ref} act={act} />}
      </>
    );
  }

  if (s) {
    const steps = s.steps.filter((st) => st.status !== "dropped");
    const cur = steps.find((st) => st.status === "running") ?? steps.find((st) => st.status === "waiting");
    return (
      <>
        <p className="mod-kicker">{t("espace.session")}, {s.state}</p>
        <h2 className="mod-title">{s.title}</h2>
        {short && cur && <p className="mod-meta">{cur.title}</p>}
        {!short && (
          <ol className="timeline">
            {steps.map((st) => (
              <li key={st.key} className={`tl ${stepClass(st.status)}`}>
                <span className="step-dot" />
                <span>{st.title}</span>
              </li>
            ))}
          </ol>
        )}
        {big && (s.summary || s.goal) && <p className="mod-text">{s.summary || s.goal}</p>}
        {!full && !short && <div className="actions"><button className="btn" onClick={() => act.open(o.ref)}>{t("espace.session.open")}</button></div>}
      </>
    );
  }

  if (k) {
    return (
      <>
        <p className="mod-kicker">{t("espace.task")}, {k.state}</p>
        <h2 className="mod-title">{k.title}</h2>
        {!short && <p className="mod-meta">{[k.agent, k.node].filter(Boolean).join(", ")}</p>}
        {full && <div className="actions"><a className="btn" href={`/tasks/${encodeURIComponent(k.id)}`}>{t("espace.task.open")}</a></div>}
      </>
    );
  }

  if (p) {
    return (
      <>
        <p className="mod-kicker">{t("espace.project")}, {p.state}</p>
        <h2 className="mod-title">{p.name}</h2>
        {!short && <p className="mod-text">{p.text}</p>}
      </>
    );
  }

  if (o.group) {
    const objs = o.group.refs.map((r) => all.find((x) => x.ref === r)).filter((x): x is Obj => !!x);
    return (
      <>
        <p className="mod-kicker">{o.title}</p>
        {short ? (
          <p className="mod-title">{t("espace.group.count", objs.length)}</p>
        ) : (
          <ul className="group">
            {objs.map((x) => (
              <li key={x.ref}>
                <button className="linkish" onClick={() => act.open(x.ref)}>{lineOf(x)}</button>
              </li>
            ))}
          </ul>
        )}
      </>
    );
  }

  return (
    <>
      <p className="mod-kicker">{o.kind}</p>
      <h2 className="mod-title">{o.title}</h2>
    </>
  );
}

export function YesNo({ ref_, act }: { ref_: Ref; act: Act }) {
  const { t } = useWords();
  return (
    <div className="actions">
      <button className="btn primary" onClick={() => act.answer(ref_, true)}>{t("espace.yes")}</button>
      <button className="btn" onClick={() => act.answer(ref_, false)}>{t("espace.no")}</button>
    </div>
  );
}

// One line for any object: a group's members, a chip.
export function lineOf(x: Obj): ReactNode {
  if (x.mail) return <><b>{nameOf(x.mail.from)}</b> {x.mail.subject}</>;
  if (x.session) return <><b>{x.session.title}</b> {x.session.steps.find((s) => s.status === "running")?.title ?? ""}</>;
  if (x.card) return x.card.text;
  return x.title;
}

// Where an object comes from, for a chip.
export function sourceOf(x: Obj): string {
  if (x.mail) return nameOf(x.mail.from);
  if (x.event) return x.event.calendar;
  if (x.task) return x.task.agent ?? "";
  return "";
}

// How Eriphe names an object when it presents it.
export function titleOf(t: T, lang: "fr" | "en", o: Obj): string {
  if (o.mail) return t("espace.say.mail", nameOf(o.mail.from));
  if (o.agenda) return t("espace.say.agenda", dayLabel(lang, o.agenda.date));
  if (o.event) return t("espace.say.event", o.event.title);
  if (o.card) return t("espace.say.card", o.card.text);
  if (o.session) return t("espace.say.session", o.session.title);
  if (o.task) return t("espace.say.task", o.task.title);
  if (o.project) return t("espace.say.project", o.project.name);
  return t("espace.say.group", o.title);
}

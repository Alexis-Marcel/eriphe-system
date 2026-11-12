import type { ReactNode } from "react";
import { ghostsFor, type Size } from "./engine";
import { clock, dayLabel, shortDate, useWords, type T } from "./i18n";
import type { Obj, Ref } from "./types";

// One view per type of object, never per situation. Each has a short form
// (size s) and a full form (large sizes, or the focus).

export type Act = {
  open: (ref: Ref) => void;
  answer: (ref: Ref, yes: boolean) => void;
  pick: (ref: Ref, option: string) => void;
  ask: (text: string) => void;
};

const nameOf = (from: string) => from.split(" <")[0];

export function View({ o, size, full, all, act }: { o: Obj; size: Size; full: boolean; all: Obj[]; act: Act }) {
  const { t, lang } = useWords();
  const short = size === "s" && !full;
  const big = full || size === "xl" || size === "l";
  switch (o.type) {
    case "mail":
      return (
        <>
          <p className="mod-kicker">{nameOf(o.from)}, {clock(lang, o.at)}</p>
          <h2 className="mod-title">{o.subject}</h2>
          {!short && <p className={`mod-text ${full ? "mail-body" : ""}`}>{o.excerpt}</p>}
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

    case "agenda": {
      const ghosts = ghostsFor(o.date, all);
      const rows = [
        ...o.events.map((e) => ({ e, kind: "real", conflict: false })),
        ...ghosts.map((g) => ({ e: g.ev, kind: g.state === "yes" ? "real" : g.state === "no" ? "" : "ghost", conflict: g.conflict && g.state === "pending" })),
      ]
        .filter((r) => r.kind)
        .sort((a, b) => a.e.start.localeCompare(b.e.start));
      return (
        <>
          <p className="mod-kicker">{dayLabel(lang, o.date)}</p>
          {rows.length === 0 && <p className="mod-meta">{t("espace.agenda.none")}</p>}
          {short && rows.length > 0 && <p className="mod-title">{t("espace.agenda.count", rows.length)}</p>}
          {!short && (
            <ol className="events">
              {rows.map(({ e, kind, conflict }) => (
                <li key={e.start + e.title} className={`ev ${kind} ${conflict ? "conflict" : ""}`}>
                  <span className="ev-time">{clock(lang, e.start)}{e.end ? `–${clock(lang, e.end)}` : ""}</span>
                  <span className="ev-title">{e.title}{kind === "ghost" ? ` (${t("espace.ev.proposed")})` : ""}</span>
                  {conflict && <span className="ev-warn">{t("espace.ev.overlap")}</span>}
                </li>
              ))}
            </ol>
          )}
        </>
      );
    }

    case "event":
      return (
        <>
          <p className="mod-kicker">{t("espace.event")}, {shortDate(lang, o.start)}</p>
          <h2 className="mod-title">{o.title}</h2>
          <p className="mod-meta">{clock(lang, o.start)}{o.end ? `–${clock(lang, o.end)}` : ""}{o.where && !short ? `, ${o.where}` : ""}</p>
        </>
      );

    case "card":
      return (
        <>
          <p className="mod-kicker">{t("espace.card.kicker", clock(lang, o.since))}</p>
          <h2 className="mod-title">{o.text}</h2>
          {!short && <p className="mod-meta">{o.tool}{o.input.start ? `, ${shortDate(lang, o.input.start)} ${clock(lang, o.input.start)}` : ""}{o.input.location ? `, ${o.input.location}` : ""}</p>}
          {!short && o.state === "pending" && <YesNo ref_={o.ref} act={act} />}
          {o.state !== "pending" && <p className="mod-meta done">{t(o.state === "yes" ? "espace.card.yes" : "espace.card.no")}</p>}
        </>
      );

    case "session": {
      const cur = o.steps.find((s) => s.status === "running") ?? o.steps.find((s) => s.status === "waiting");
      return (
        <>
          <p className="mod-kicker">{t("espace.session")}{o.project ? `, ${o.project}` : ""}</p>
          <h2 className="mod-title">{o.title}</h2>
          {short && cur && <p className="mod-meta">{cur.title}</p>}
          {!short && (
            <ol className="timeline">
              {o.steps.map((s) => (
                <li key={s.title} className={`tl ${s.status}`}>
                  <span className="step-dot" />
                  <span>{s.at && <b>{clock(lang, s.at)} </b>}{s.title}{big && s.note ? <em>{s.note}</em> : null}</span>
                </li>
              ))}
            </ol>
          )}
          {big && o.finding && <p className="mod-text">{o.finding}</p>}
          {!full && !short && <div className="actions"><button className="btn" onClick={() => act.open(o.ref)}>{t("espace.session.open")}</button></div>}
        </>
      );
    }

    case "choice":
      return (
        <>
          <p className="mod-kicker">{t("espace.choice.kicker")}</p>
          <h2 className={`mod-title ${big ? "big" : ""}`}>{o.question}</h2>
          {o.advice && !short && <p className="mod-text">{o.advice}</p>}
          {!short && (
            <div className="options">
              {o.options.map((opt) => (
                <button key={opt} className={`option ${o.picked === opt ? "picked" : ""}`} onClick={() => act.pick(o.ref, opt)}>
                  <span className="option-label">{opt}</span>
                </button>
              ))}
            </div>
          )}
        </>
      );

    case "alert":
      return (
        <>
          <p className="mod-kicker">{o.monitor}, {o.severity}</p>
          <h2 className="mod-title">{o.title}</h2>
          <p className="mod-meta">{clock(lang, o.at)}{o.count > 1 ? `, ${t("espace.alert.count", o.count)}` : ""}</p>
          {!short && o.note && <p className="mod-text">{o.note}</p>}
        </>
      );

    case "project":
      return (
        <>
          <p className="mod-kicker">{t("espace.project")}, {o.state}</p>
          <h2 className="mod-title">{o.name}</h2>
          <p className={`mod-meta ${o.late ? "late" : ""}`}>
            {o.due ? t(o.late ? "espace.project.late" : "espace.project.due", shortDate(lang, o.due)) : t("espace.project.nodue")}
          </p>
          {!short && <p className="mod-text">{o.next}</p>}
        </>
      );

    case "group": {
      const objs = o.refs.map((r) => all.find((x) => x.ref === r)).filter((x): x is Obj => !!x);
      return (
        <>
          <p className="mod-kicker">{o.title}</p>
          {short ? (
            <p className="mod-title">{t("espace.group.count", objs.length)}</p>
          ) : (
            <ul className="group">
              {objs.map((x) => (
                <li key={x.ref}>
                  <button className="linkish" onClick={() => act.open(x.ref)}>{lineOf(t, x)}</button>
                </li>
              ))}
            </ul>
          )}
        </>
      );
    }
  }
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
export function lineOf(t: T, x: Obj): ReactNode {
  switch (x.type) {
    case "mail":
      return <><b>{nameOf(x.from)}</b> {x.subject}</>;
    case "session":
      return <><b>{x.project ?? x.title}</b> {x.steps.find((s) => s.status !== "done")?.title ?? ""}</>;
    case "agenda":
      return x.date;
    case "card":
      return x.text;
    case "choice":
      return x.question;
    case "event":
    case "alert":
      return x.title;
    case "project":
      return x.name;
    case "group":
      return <>{x.title} ({t("espace.group.count", x.refs.length)})</>;
  }
}

// Where an object comes from, for a chip.
export function sourceOf(x: Obj): string {
  switch (x.type) {
    case "mail":
      return nameOf(x.from);
    case "card":
      return x.tool;
    case "alert":
      return x.monitor;
    case "session":
      return x.project ?? "";
    case "event":
      return x.source ?? "";
    default:
      return "";
  }
}

// How Eriphe names an object when it presents it.
export function titleOf(t: T, lang: "fr" | "en", o: Obj): string {
  switch (o.type) {
    case "mail":
      return t("espace.say.mail", nameOf(o.from));
    case "agenda":
      return t("espace.say.agenda", dayLabel(lang, o.date));
    case "event":
      return t("espace.say.event", o.title);
    case "card":
      return t("espace.say.card", o.text);
    case "session":
      return t("espace.say.session", o.title);
    case "choice":
      return t("espace.say.choice");
    case "alert":
      return t("espace.say.alert", o.title);
    case "project":
      return t("espace.say.project", o.name);
    case "group":
      return t("espace.say.group", o.title);
  }
}

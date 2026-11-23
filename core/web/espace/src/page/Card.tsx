import { useContext, useEffect, useState } from "react";
import { Icon as IconView } from "../a2ui/icons";
import { Markdown as MarkdownView } from "../a2ui/markdown";
import { HomeNode, useMoved, useProposals, type ProposalView } from "../a2ui/proposals";
import { stillAsks } from "../data/api";
import { useWords } from "../i18n";

// Card draws one proposal of Eriphe's, read from eriphe-core by its ref
// (kind:id), and again when it moved: code's own, never a block's (no
// component of the catalog says yes). Its one Oui and its discreet ✕
// answer that proposal's card, as anywhere else (its row, a channel, a
// word). A card of several calls lists each with a box, all ticked: Oui
// runs those ticked, refuses the others, and says what it does for them.
// On its own node's page (HomeNode: the page's node, code's), where it
// comes from and what Oui does in general go under Détails.

// KEEPS are the kinds of proposals that only ask whether to keep something
// (a memory, a reminder): yes runs nothing. Their header asks it and their
// buttons say Garder and Oublier; any other kind does something once
// accepted: Proposition, Oui and ✕.
const KEEPS: Record<string, string> = { memory: "espace.a2.proposal.memory", intention: "espace.a2.proposal.intention" };

// Fields are a proposal's fields, by name: a Markdown value (a skill's
// instructions) drawn as Markdown, in a box that scrolls when long.
function Fields({ fields }: { fields: NonNullable<ProposalView["fields"]> }) {
  const { t } = useWords();
  if (fields.length === 0) return null;
  return (
    <dl className="a2-proposal-fields">
      {fields.map((f) => (
        <div key={f.name} className={f.markdown ? "a2-proposal-md" : undefined}>
          <dt>{f.name}</dt>
          <dd>{f.markdown ? <MarkdownView text={f.value} newTab={t("espace.a2.newtab")} /> : f.value}</dd>
        </div>
      ))}
    </dl>
  );
}

export function Card({ refText, surface }: { refText: string; surface: string }) {
  const { t } = useWords();
  const homeNode = useContext(HomeNode);
  const src = useProposals();
  const ref = refText;
  const [p, setP] = useState<ProposalView | null | undefined>(undefined);
  const [busy, setBusy] = useState<"yes" | "no" | null>(null); // the answer on its way
  const [failed, setFailed] = useState(false);
  const [left, setLeft] = useState<ReadonlySet<number>>(() => new Set()); // the calls unticked
  useEffect(() => {
    let live = true;
    setP(undefined);
    src.read(ref).then(
      (v) => live && setP(v),
      () => live && setP(null),
    );
    return () => {
      live = false;
    };
  }, [src, ref]);
  // Answered elsewhere (its row, a channel, another page): read again.
  const moved = useMoved(src, [ref]);
  useEffect(() => {
    if (!moved) return;
    let live = true;
    src.read(ref).then(
      (v) => live && setP(v),
      () => {},
    );
    return () => {
      live = false;
    };
  }, [src, ref, moved]);
  const choices = p?.choices ?? [];
  const choosing = p?.state === "pending" && choices.length > 1;
  const count = choices.filter((c) => !left.has(c.id)).reduce((n, c) => n + c.n, 0);
  const answer = async (yes: boolean) => {
    if (!p || busy) return;
    setBusy(yes ? "yes" : "no");
    setFailed(false);
    // A Oui names what the card shows, even one call: a call folded in
    // since is in neither list, and waits on.
    const chosen = yes && p.state === "pending" && choices.length > 0 ? { only: choices.filter((c) => !left.has(c.id)).map((c) => c.id), left: choices.filter((c) => left.has(c.id)).map((c) => c.id) } : {};
    const ok = await src.answer({ surfaceId: surface, componentId: ref, ref: p.ref, yes, ...chosen }).catch(() => false);
    if (!ok) setFailed(true);
    const again = await src.read(ref).catch(() => null);
    setP(again ?? { ...p, state: ok ? (yes ? "approved" : "rejected") : p.state });
    setBusy(null);
  };
  if (p === undefined)
    return (
      <div className="a2-proposal a2-proposal-loading" aria-busy="true">
        <span className="a2-shimmer" aria-hidden="true" />
      </div>
    );
  if (p === null) return <p className="a2-empty">{t("espace.a2.proposal.none")}</p>;
  // Decided (done, failed, declined, withdrawn), it leaves: the page's ⋯
  // menu lists it, and the orb says how its yes went.
  if (!stillAsks(p)) return null;
  const pending = p.state === "pending";
  const keeps = KEEPS[p.kind ?? ""];
  // Approved, an action's call runs (the stream's "proposals" moves bring
  // it), until it is done or failed, and the card leaves.
  const exec = pending ? undefined : "running";
  const kicker = pending ? t(keeps ?? "espace.a2.proposal") : t("espace.a2.proposal.running");
  // On its own node's page, where it comes from (its node, its skill) and
  // what Oui does in general are under Détails: the page says it.
  const home = Boolean(homeNode && p.node === homeNode);
  // A call's origin (the skill that kept it) is a quiet line, never its
  // subtitle: beside where it comes from, under Détails on its own page.
  const aside = p.kind === "calls" || p.kind === "runcalls" ? p.why : undefined;
  const why = aside ? undefined : p.why;
  // Its origin quotes words that may be someone else's (fromExternal):
  // shown as eriphe-core marked them, in the external amber.
  const said = p.fromExternal ? (
    <span className="a2-from-ext" title={t("espace.words.external")}>
      {p.from}
    </span>
  ) : (
    p.from
  );
  const from = p.from && (p.node ? <a href={`/espace?open=node-${p.node}`}>{said}</a> : said);
  const yesWord = choosing && p.acts && count > 0 ? p.acts[Math.min(count, p.acts.length) - 1] : t(keeps ? "espace.a2.keep" : "espace.a2.yes");
  const raw = (p.fields ?? []).filter((f) => f.detail && f.raw);
  const details = Boolean(p.fields?.some((f) => f.detail) || (home && (aside || p.from || (pending && p.yes))));
  const icon = pending ? "star" : "clock";
  return (
    <section className={`a2-proposal a2-proposal-${pending ? "pending" : "decided"}${exec ? " a2-proposal-exec-" + exec : ""}`} aria-label={t(keeps ?? "espace.a2.proposal")} aria-busy={exec === "running" || undefined}>
      <p className="a2-proposal-kicker" role={exec ? "status" : undefined}>
        <IconView name={icon} />
        {kicker}
        {/* Drawn from external content: a small amber sign, no word; its
            whole sentence on hover and for a screen reader. */}
        {p.tainted && (
          <span className="a2-taint" role="img" title={t("espace.a2.proposal.tainted")} aria-label={t("espace.a2.proposal.tainted")}>
            <IconView name="warning" />
          </span>
        )}
      </p>
      <p className="a2-proposal-text">
        {p.text}
        {p.context && <span className="a2-proposal-context"> · {p.context}</span>}
      </p>
      {why && <p className="a2-proposal-why">{why}</p>}
      {p.summary && p.summary.length > 0 && (
        <ul className="a2-proposal-summary">
          {p.summary.map((s) => (
            <li key={s}>{s}</li>
          ))}
        </ul>
      )}
      {choices.length > 1 && (
        <ul className="a2-proposal-choices" aria-label={t("espace.a2.proposal.choices")}>
          {choices.map((c) =>
            choosing ? (
              <li key={c.id}>
                <label>
                  <input
                    type="checkbox"
                    checked={!left.has(c.id)}
                    disabled={!!busy}
                    onChange={(e) => {
                      const on = e.target.checked;
                      setLeft((was) => {
                        const next = new Set(was);
                        if (on) next.delete(c.id);
                        else next.add(c.id);
                        return next;
                      });
                    }}
                  />
                  <span>{c.text}</span>
                </label>
              </li>
            ) : (
              <li key={c.id} className={c.state === "rejected" ? "is-left" : undefined}>
                <span>{c.text}</span>
                {c.state === "rejected" && <small>{t("espace.a2.proposal.left")}</small>}
              </li>
            ),
          )}
        </ul>
      )}
      <Fields fields={(p.fields ?? []).filter((f) => !f.detail)} />
      {details && (
        <details className="a2-proposal-details">
          <summary>{t("espace.a2.proposal.details")}</summary>
          {home && aside && <p className="a2-proposal-from">{aside}</p>}
          {home && from && <p className="a2-proposal-from">{from}</p>}
          {home && pending && p.yes && <p className="a2-proposal-yes">{p.yes}</p>}
          <Fields fields={(p.fields ?? []).filter((f) => f.detail && !f.raw)} />
          {/* Its exact call, one click further: its parameters as the
              tool reads them. */}
          {raw.length > 0 && (
            <details className="a2-proposal-raw">
              <summary>{t("espace.a2.proposal.raw")}</summary>
              <Fields fields={raw} />
            </details>
          )}
        </details>
      )}
      {!home && (from || aside) && (
        <p className="a2-proposal-from">
          {from}
          {from && aside && " · "}
          {aside}
        </p>
      )}
      {!home && pending && p.yes && <p className="a2-proposal-yes">{p.yes}</p>}
      {pending && (
        <div className="a2-proposal-actions">
          <button type="button" className={`btn a2-button a2-button-primary${busy === "yes" ? " is-busy" : ""}`} disabled={!!busy || (choosing && count === 0)} aria-busy={busy === "yes"} onClick={() => answer(true)}>
            {yesWord}
          </button>
          <button
            type="button"
            className={`a2-inline-no a2-proposal-no${busy === "no" ? " is-busy" : ""}`}
            disabled={!!busy}
            aria-busy={busy === "no"}
            title={t(keeps ? "espace.a2.forget" : "espace.a2.no")}
            aria-label={t(keeps ? "espace.a2.forget" : "espace.a2.no")}
            onClick={() => answer(false)}
          >
            <IconView name="close" />
          </button>
        </div>
      )}
      {failed && (
        <p className="a2-proposal-failed" role="alert">
          {t("espace.a2.proposal.failed")}
        </p>
      )}
    </section>
  );
}

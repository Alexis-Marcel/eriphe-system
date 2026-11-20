import { useEffect, useState, type FormEvent } from "react";
import * as api from "../data/api";
import { lastSeq, setSent, step, store, useGraph } from "../data/store";
import { titleOf } from "../graph/model";
import { useWords } from "../i18n";
import { Cards } from "../page/Cards";
import { Line } from "./Line";
import { orbText, useOrb } from "./useOrb";

// The bar: the one input of the workspace, and only a bar. It talks to
// the page the store's talk names, else to the home. Eriphe answers with
// its orb (Orb.tsx): beside the subject its words belong to, at the home
// when none; the bar holds only its resting place (cs-dock), where the orb
// waits when it has nothing to say. Above the field: the user's messages
// that wait for Eriphe, each with a clock, the node it was said to when
// another, and a ✕ that withdraws it while Eriphe has not read it; the
// cards no node holds (one proposed at the home, before any subject), so
// that none waits out of sight; on the phone, the orb's words as a line
// (Line.tsx). And what the bar itself has to say: a message that could
// not go, one withdrawn too late, an Eriphe that cannot think yet.

// WAITING_SHOWN is how many waiting messages the bar shows, the newest:
// the others are counted.
export const WAITING_SHOWN = 3;

export function waitingShown(x: api.OrbFeed | null, gone: ReadonlySet<number>): { shown: api.Waiting[]; more: number } {
  const all = (x?.waiting ?? []).filter((w) => !gone.has(w.seq));
  const shown = all.slice(-WAITING_SHOWN);
  return { shown, more: all.length - shown.length };
}

export function Bar({ boot, narrow }: { boot: api.Boot; narrow: boolean }) {
  const { t } = useWords();
  const talk = useGraph((s) => s.talk);
  const node = useGraph((s) => (s.talk ? s.nodes[s.talk] : undefined));
  const nodes = useGraph((s) => s.nodes);
  const x = useGraph((s) => s.orb);
  const { show, crowded } = useOrb();
  const [draft, setDraft] = useState("");
  const [failed, setFailed] = useState(false);
  const cards = x?.cards ?? [];
  const [gone, setGone] = useState<ReadonlySet<number>>(new Set());
  const [taken, setTaken] = useState(false);
  const { shown: waiting, more } = waitingShown(x, gone);
  useEffect(() => {
    if (!taken) return;
    const t = setTimeout(() => setTaken(false), 4000);
    return () => clearTimeout(t);
  }, [taken]);
  const withdraw = async (seq: number) => {
    setGone((g) => new Set(g).add(seq));
    const r = await api.withdraw(seq);
    if (r === "ok") return;
    setGone((g) => {
      const n = new Set(g);
      n.delete(seq);
      return n;
    });
    if (r === "taken") setTaken(true);
    else setFailed(true);
  };

  const place = talk ? titleOf(node, t("espace.external")) : null;
  const say = async (text: string) => {
    const words = text.trim();
    if (!words) return;
    setDraft("");
    setFailed(false);
    if (!boot.live) return;
    step(store, setSent, { text: words, after: lastSeq(store.getState().orb), to: talk });
    const ok = await api.send(words, talk).catch(() => false);
    if (!ok) {
      step(store, setSent, null);
      setDraft(words);
      setFailed(true);
    }
  };
  const onSubmit = (e: FormEvent) => {
    e.preventDefault();
    void say(draft);
  };
  const live = orbText(show, t, nodes);

  return (
    <div className="cs">
      {/* What the orb says, for a screen reader: here, where it stays. */}
      <p className="sr-only" role="status" aria-live="polite" aria-label={t("espace.orb.words")}>
        {live}
      </p>
      <div className="cs-aside">
        {cards.length > 0 && (
          <div className="cs-cards" aria-label={t("espace.bar.cards")}>
            <Cards surface="bar--cards" refs={cards} />
          </div>
        )}
        {waiting.length > 0 && (
          <ol className="cs-waiting" aria-label={t("espace.bar.waiting")}>
            {more > 0 && <li className="cs-wait-more">{t("espace.bar.waiting.more", more)}</li>}
            {waiting.map((w) => (
              <li key={w.seq} className="cs-wait">
                <Clock label={t("espace.bar.waiting.one")} />
                <span className="cs-wait-text" title={w.text}>
                  {w.text}
                </span>
                {(w.node ?? null) !== talk && <span className="cs-wait-node">{w.node ? w.title || t("espace.external") : t("espace.home")}</span>}
                <button type="button" className="cs-wait-x" aria-label={t("espace.bar.withdraw")} title={t("espace.bar.withdraw")} onClick={() => void withdraw(w.seq)}>
                  ✕
                </button>
              </li>
            ))}
          </ol>
        )}
        {(narrow || crowded) && <Line mini={narrow} />}
      </div>
      {!boot.live && <p className="cs-note">{t("espace.sent.sample")}</p>}
      {boot.setup === "noai" && (
        <p className="cs-note" role="status">
          {t("espace.noai")} <a href="/welcome">{t("espace.noai.link")}</a>
        </p>
      )}
      {boot.setup === "nobrain" && (
        <p className="cs-note" role="status">
          {t("espace.nobrain")}
        </p>
      )}
      {failed && <p className="cs-note" role="alert">{t("espace.error")}</p>}
      {taken && (
        <p className="cs-note" role="status">
          {t("espace.bar.taken")}
        </p>
      )}

      <form className="cs-bar" onSubmit={onSubmit}>
        <input
          value={draft}
          onChange={(e) => setDraft(e.target.value)}
          placeholder={place ? t("espace.input.about", place) : t("espace.input")}
          aria-label={place ? t("espace.input.about", place) : t("espace.input")}
          enterKeyHint="send"
        />
        {/* The orb's resting place: on the phone the orb stays in it. */}
        <span className="cs-dock">{narrow && <span className={`orb docked${show.mode === "work" ? " working" : ""}`} aria-hidden="true" />}</span>
      </form>
    </div>
  );
}

// Clock is the small clock of a message that waits.
function Clock({ label }: { label: string }) {
  return (
    <svg className="cs-wait-clock" viewBox="0 0 16 16" width="12" height="12" role="img" aria-label={label}>
      <circle cx="8" cy="8" r="6.25" fill="none" stroke="currentColor" strokeWidth="1.5" />
      <path d="M8 4.5V8l2.25 1.5" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round" />
    </svg>
  );
}

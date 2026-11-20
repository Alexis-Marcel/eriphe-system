import { useEffect, useState, type FormEvent } from "react";
import * as api from "../data/api";
import { useGraph } from "../data/store";
import { titleOf } from "../graph/model";
import { Cards } from "../page/Cards";
import { useWords } from "../i18n";
import { landed, lastSeq, present, useAck, waitingShown, type Sent } from "./exchange";

// The bar (#633, #663): the one input of the workspace, and only a bar.
// The subject's document is the conversation: what Eriphe has to say at
// length, its questions and the buttons that answer them are there. The
// bar talks to the page the store's talk names, else to the home; above
// its field, the user's last message in grey and one line of Eriphe's,
// the first of its answer, which goes after a few seconds. No history, no
// buttons, but one exception: the cards no node holds (one proposed at the
// home, before any subject) show above it, so that none waits out of
// sight. And the user's messages that wait for Eriphe, sent while it
// works: each in grey with a clock, the node it was said to when another,
// and a ✕ that withdraws it while Eriphe has not read it.

export function Console({ boot }: { boot: api.Boot }) {
  const { t } = useWords();
  const talk = useGraph((s) => s.talk);
  const node = useGraph((s) => (s.talk ? s.nodes[s.talk] : undefined));
  const x = useGraph((s) => s.exchange);
  const [sent, setSent] = useState<Sent | null>(null);
  const [draft, setDraft] = useState("");
  const [failed, setFailed] = useState(false);
  const { asked, said, writing, line, failed: unanswered } = present(x, sent && sent.to === talk ? sent : null);
  const shown = useAck(said ? `${talk ?? ""}#${said.seq}` : null);
  const cards = x?.cards ?? [];
  const [gone, setGone] = useState<ReadonlySet<number>>(new Set());
  const [taken, setTaken] = useState(false);
  const { shown: waiting, more } = waitingShown(x, gone);
  useEffect(() => {
    if (landed(x, sent)) setSent(null);
  }, [x, sent]);
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
    setSent({ text: words, after: lastSeq(x), to: talk });
    const ok = await api.send(words, talk).catch(() => false);
    if (!ok) {
      setSent(null);
      setDraft(words);
      setFailed(true);
    }
  };
  const onSubmit = (e: FormEvent) => {
    e.preventDefault();
    void say(draft);
  };
  // Eriphe's line: what it is writing, else its answer's first line, for
  // a few seconds.
  const ack = line ?? (said && shown ? said : null);

  return (
    <div className="cs">
      <div className="cs-aside" aria-live="polite">
        {cards.length > 0 && (
          <div className="cs-cards" aria-label={t("espace.exchange.cards")}>
            <Cards surface="console--cards" refs={cards} />
          </div>
        )}
        {waiting.length > 0 && (
          <ol className="cs-waiting" aria-label={t("espace.exchange.waiting")}>
            {more > 0 && <li className="cs-wait-more">{t("espace.exchange.waiting.more", more)}</li>}
            {waiting.map((w) => (
              <li key={w.seq} className="cs-wait">
                <Clock label={t("espace.exchange.waiting.one")} />
                <span className="cs-wait-text" title={w.text}>
                  {w.text}
                </span>
                {(w.node ?? null) !== talk && <span className="cs-wait-node">{w.node ? w.title || t("espace.external") : t("espace.home")}</span>}
                <button type="button" className="cs-wait-x" aria-label={t("espace.exchange.withdraw")} title={t("espace.exchange.withdraw")} onClick={() => void withdraw(w.seq)}>
                  ✕
                </button>
              </li>
            ))}
          </ol>
        )}
        {(asked || ack || writing) && (
          <div className="cs-panel">
            {asked && <Asked key={asked} text={asked} />}
            {ack && (
              <p key={typeof ack === "string" ? "draft" : ack.seq} className={`cs-said${typeof ack === "string" ? " draft" : ""}`}>
                {typeof ack === "string" ? ack : ack.text}
                {typeof ack !== "string" && ack.external && <span className="cs-external" title={t("espace.exchange.external")} aria-label={t("espace.exchange.external")} />}
              </p>
            )}
            {!ack && writing && (
              <p className="cs-dots" role="status" aria-label={t("espace.exchange.writing")}>
                <i />
                <i />
                <i />
              </p>
            )}
          </div>
        )}
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
      {unanswered && (
        <p className="cs-note cs-error" role="alert">
          {t("espace.exchange.error")}{" "}
          <a
            href="#"
            onClick={(e) => {
              e.preventDefault();
              void api.retryBrain().catch(() => false);
            }}
          >
            {t("espace.retry")}
          </a>
          {" · "}
          <a href="/settings/agents">{t("espace.exchange.account")}</a>
        </p>
      )}
      {failed && <p className="cs-note" role="alert">{t("espace.error")}</p>}
      {taken && (
        <p className="cs-note" role="status">
          {t("espace.exchange.taken")}
        </p>
      )}

      <form className={`cs-bar${writing ? " writing" : ""}`} onSubmit={onSubmit}>
        <input
          value={draft}
          onChange={(e) => setDraft(e.target.value)}
          placeholder={place ? t("espace.input.about", place) : t("espace.input")}
          aria-label={place ? t("espace.input.about", place) : t("espace.input")}
          enterKeyHint="send"
        />
        <span className={`cs-orb${writing || x?.draft ? " speaking" : ""}`} role="status" aria-label={writing ? t("espace.exchange.writing") : boot.name}>
          <span className="cs-orb-glow" />
          <span className="cs-wave">
            {Array.from({ length: 5 }, (_, i) => (
              <i key={i} />
            ))}
          </span>
        </span>
      </form>
    </div>
  );
}

// Asked is the user's last question, above Eriphe's line: one grey line,
// cut with « … », its whole text on a click. The next question replaces it
// (its key), folded again.
export function Asked({ text }: { text: string }) {
  const { t } = useWords();
  const [whole, setWhole] = useState(false);
  return (
    <button type="button" className={`cs-asked${whole ? " whole" : ""}`} aria-expanded={whole} title={whole ? undefined : text} onClick={() => setWhole(!whole)}>
      <span className="sr-only">{t("espace.exchange.asked")} </span>
      {text}
    </button>
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

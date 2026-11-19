import { useEffect, useLayoutEffect, useRef, useState, type FormEvent } from "react";
import { AnimatePresence, motion } from "motion/react";
import { Markdown } from "../a2ui/markdown";
import { sampleEarlier } from "../a2ui/fixtures";
import * as api from "../data/api";
import { useGraph } from "../data/store";
import { titleOf } from "../graph/model";
import { Cards } from "../page/Cards";
import { useWords } from "../i18n";
import { landed, lastSeq, present, type Sent } from "./exchange";

// The console (#633): the one input of the workspace. It talks to the page
// the console talks to (the store's talk), else to the home; Eriphe's
// newest line shows by the orb, the replies it suggests under it. The
// conversation of that node opens over it, its lines before on request.
// The cards no node holds (one proposed at the home, before any subject)
// show with it, open or not, so that none waits out of sight.

const spring = { type: "spring", stiffness: 300, damping: 32 } as const;

export function Console({ boot }: { boot: api.Boot }) {
  const { t } = useWords();
  const talk = useGraph((s) => s.talk);
  const node = useGraph((s) => (s.talk ? s.nodes[s.talk] : undefined));
  const x = useGraph((s) => s.exchange);
  const [sent, setSent] = useState<Sent | null>(null);
  const [draft, setDraft] = useState("");
  const [conv, setConv] = useState(false);
  const [failed, setFailed] = useState(false);
  const { items, writing, replies, aside } = present(x, sent && sent.to === talk ? sent : null);
  const cards = x?.cards ?? [];
  useEffect(() => {
    if (landed(x, sent)) setSent(null);
  }, [x, sent]);

  const place = talk ? titleOf(node, t("espace.external")) : null;
  const say = async (text: string, from: "workspace" | "suggestion" = "workspace") => {
    const said = text.trim();
    if (!said) return;
    setDraft("");
    setFailed(false);
    if (!boot.live) return;
    setSent({ text: said, after: lastSeq(x), to: talk });
    const ok = await api.send(said, talk, from).catch(() => false);
    if (!ok) {
      setSent(null);
      setDraft(said);
      setFailed(true);
    }
  };
  const onSubmit = (e: FormEvent) => {
    e.preventDefault();
    void say(draft);
  };

  return (
    <div className="cs">
      <AnimatePresence>
        {conv && (
          <motion.section className="cs-conv" initial={{ opacity: 0, y: 16, scale: 0.98 }} animate={{ opacity: 1, y: 0, scale: 1 }} exit={{ opacity: 0, y: 12 }} transition={spring}>
            <header>
              <span className="micro">{place ?? t("espace.home")}</span>
              <button type="button" onClick={() => setConv(false)} aria-label={t("espace.page.close")}>
                ✕
              </button>
            </header>
            <Lines live={boot.live} talk={talk} x={x} items={items} writing={writing} />
            {cards.length > 0 && (
              <div className="cs-cards" aria-label={t("espace.exchange.cards")}>
                <Cards surface="console--cards" refs={cards} />
              </div>
            )}
          </motion.section>
        )}
      </AnimatePresence>

      {!conv && (
        <div className="cs-aside" aria-live="polite">
          <AnimatePresence mode="wait">
            {aside && (
              <motion.div key={`${talk}-${aside.slice(0, 40)}`} initial={{ opacity: 0, y: 8 }} animate={{ opacity: 1, y: 0 }} exit={{ opacity: 0, y: -4 }} transition={{ duration: 0.35 }}>
                <div className="cs-said">
                  <Markdown text={aside} newTab={t("espace.a2.newtab")} />
                </div>
              </motion.div>
            )}
          </AnimatePresence>
          {replies.length > 0 && (
            <div className="cs-replies" role="group" aria-label={t("espace.exchange.replies")}>
              {replies.map((r) => (
                <button key={r} type="button" className="cs-reply" onClick={() => void say(r, "suggestion")}>
                  {r}
                </button>
              ))}
            </div>
          )}
          {cards.length > 0 && (
            <div className="cs-cards" aria-label={t("espace.exchange.cards")}>
              <Cards surface="console--cards" refs={cards} />
            </div>
          )}
        </div>
      )}
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

      <form className={`cs-bar${writing ? " writing" : ""}`} onSubmit={onSubmit}>
        <button type="button" className="cs-conv-btn" onClick={() => setConv(!conv)} aria-pressed={conv} aria-label={t("espace.exchange")}>
          <svg viewBox="0 0 24 24" width="15" height="15" aria-hidden="true">
            <path d="M4 5h16v10H9l-5 4V5Z" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinejoin="round" />
          </svg>
        </button>
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

// Lines is the conversation of the node the console talks to: the lines
// brought before the last exchange, a few at a time, then the exchange.
function Lines({ live, talk, x, items, writing }: { live: boolean; talk: string | null; x: api.Exchange | null; items: ReturnType<typeof present>["items"]; writing: boolean }) {
  const { t, lang } = useWords();
  const head = x?.lines[0]?.seq ?? 0;
  const [before, setBefore] = useState<{ key: string; lines: api.ExchangeLine[]; more: boolean }>({ key: "", lines: [], more: false });
  const key = `${talk}@${head}`;
  const mine = before.key === key ? before : { key, lines: [], more: !!x?.earlier };
  const [busy, setBusy] = useState(false);
  const box = useRef<HTMLDivElement>(null);
  useLayoutEffect(() => {
    const b = box.current;
    if (b && typeof b.scrollTo === "function") b.scrollTo({ top: b.scrollHeight });
  }, [items.length, writing]);
  const bring = async () => {
    const first = mine.lines[0]?.seq ?? head;
    if (!first || busy) return;
    setBusy(true);
    try {
      const got = live ? await api.earlier(first, talk ?? undefined) : sampleEarlier(lang, first);
      setBefore({ key, lines: [...got.lines, ...mine.lines], more: got.more && got.lines.length > 0 });
    } catch {
      /* not this time: the button stays */
    } finally {
      setBusy(false);
    }
  };
  return (
    <div className="cs-lines" ref={box} tabIndex={0} aria-label={t("espace.exchange")}>
      {mine.more && head > 0 && (
        <button type="button" className="cs-earlier" onClick={bring} disabled={busy}>
          {t("espace.exchange.earlier")}
        </button>
      )}
      {mine.lines.map((l) => (
        <Line key={l.seq} line={l} />
      ))}
      {items.map((it, i) => (it.kind === "draft" ? <Line key="draft" line={{ seq: 0, role: "brain", text: it.text, time: "" }} draft /> : <Line key={it.line.seq === -1 ? `sent-${i}` : it.line.seq} line={it.line} />))}
      {writing && (
        <p className="cs-dots" role="status" aria-label={t("espace.exchange.writing")}>
          <i />
          <i />
          <i />
        </p>
      )}
    </div>
  );
}

function Line({ line, draft }: { line: api.ExchangeLine; draft?: boolean }) {
  const { t } = useWords();
  if (line.role === "error")
    return (
      <p className="cs-error" role="alert">
        {t("espace.exchange.error")}{" "}
        <button type="button" className="linkish" onClick={() => void api.retryBrain().catch(() => false)}>
          {t("espace.retry")}
        </button>
        {" · "}
        <a href="/settings/agents">{t("espace.exchange.account")}</a>
      </p>
    );
  if (line.role === "user")
    return (
      <div className="cs-msg you">
        {line.via && <span className="cs-via">{t("espace.exchange.via", line.via)}</span>}
        <p>{line.text}</p>
      </div>
    );
  return (
    <div className={`cs-msg eri${draft ? " draft" : ""}`}>
      <Markdown text={line.text ?? ""} newTab={t("espace.a2.newtab")} />
      {line.external && <p className="cs-external">{t("espace.exchange.external")}</p>}
    </div>
  );
}

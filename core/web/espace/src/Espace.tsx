import { useCallback, useEffect, useMemo, useState, type FormEvent } from "react";
import { AnimatePresence, LayoutGroup, MotionConfig, motion, useReducedMotion } from "motion/react";
import * as data from "./data";
import { keysOf, place } from "./engine";
import { useWords } from "./i18n";
import type { Composition, Obj, Ref, Rule } from "./types";
import { View, YesNo, lineOf, sourceOf, titleOf, type Act } from "./views";

const EMPTY: Composition = { mode: "side", eriphe: "", items: [] };

const spring = { type: "spring", stiffness: 260, damping: 30 } as const;

// What the user types to be walked through, in either language.
const PRESENT = /pr[ée]sente|d[ée]roule|walk me|present/i;

function useTyped(text: string) {
  const reduced = useReducedMotion();
  const [n, setN] = useState(0);
  useEffect(() => {
    if (reduced) {
      setN(text.length);
      return;
    }
    setN(0);
    const timer = setInterval(() => setN((v) => (v >= text.length ? (clearInterval(timer), v) : v + 3)), 16);
    return () => clearInterval(timer);
  }, [text, reduced]);
  return text.slice(0, n);
}

export function Espace({ boot }: { boot: data.Boot }) {
  const { t, lang } = useWords();
  const [snap, setSnap] = useState<data.Snapshot | null>(null);
  const [failed, setFailed] = useState(false);
  const [focus, setFocus] = useState<Ref | null>(null);
  const [decided, setDecided] = useState<Record<Ref, string>>({}); // cards answered here
  const [said, setSaid] = useState<string | null>(null);
  const [toast, setToast] = useState<string | null>(null);
  const [menu, setMenu] = useState<Ref | null>(null);
  const [rules, setRules] = useState<Rule[]>(data.loadRules);
  const [tour, setTour] = useState<{ refs: Ref[]; i: number } | null>(null);
  const [wide, setWide] = useState(false);

  const reload = useCallback(() => {
    data.load(boot).then((s) => { setSnap(s); setFailed(false); }, () => setFailed(true));
  }, [boot]);
  useEffect(reload, [reload]);
  useEffect(() => (snap && !snap.sample ? data.follow(reload) : undefined), [snap?.sample, reload]);
  useEffect(() => data.saveRules(rules), [rules]);

  const store = useMemo(() => {
    const m = new Map<Ref, Obj>();
    for (const [ref, o] of snap?.store ?? []) m.set(ref, o.card && decided[ref] ? { ...o, card: { ...o.card, state: decided[ref] } } : o);
    return m;
  }, [snap, decided]);
  const all = useMemo(() => [...store.values()], [store]);

  const comp = snap?.comp ?? EMPTY;
  const { placed, chips } = useMemo(
    () => place(comp, store, rules, focus),
    [comp, store, rules, focus],
  );
  const shownChips = chips.filter((c) => !(c.obj.card && c.obj.card.state !== "pending"));

  const fp = focus ? placed.find((p) => p.focus) : undefined;
  const focusSay = fp ? fp.say ?? `${t("espace.say.lead", titleOf(t, lang, fp.obj))}${fp.why ? ` ${fp.why}.` : ""}` : null;
  const text = useTyped(failed ? t("espace.error") : !snap ? "" : said ?? focusSay ?? comp.eriphe);

  const say = (m: string) => {
    setToast(m);
    setTimeout(() => setToast((x) => (x === m ? null : x)), 2600);
  };
  const open = (ref: Ref | null) => {
    setFocus(ref);
    setMenu(null);
    setSaid(null);
  };

  // « Présente-moi »: Eriphe walks through what is on the screen, the most
  // important first, one module at a time.
  const present = () => {
    const refs = placed.filter((p) => p.weight >= 2 || p.size !== "s").map((p) => p.obj.ref);
    if (!refs.length) return;
    setTour({ refs, i: 0 });
    open(refs[0]);
  };
  const step = (d: number) => {
    if (!tour) return;
    const i = tour.i + d;
    if (i < 0) return;
    if (i >= tour.refs.length) {
      setTour(null);
      open(null);
      setSaid(t("espace.tour.end"));
      return;
    }
    setTour({ ...tour, i });
    open(tour.refs[i]);
  };
  const stopTour = () => {
    setTour(null);
    open(null);
  };

  const send = async (msg: string, done: string) => {
    if (snap?.sample || !boot.live) return say(t("espace.sent.sample"));
    say((await data.send(msg).catch(() => false)) ? done : t("espace.error"));
  };

  const act: Act = {
    open,
    answer: async (ref, yes) => {
      if (!snap?.sample && !(await data.answer(ref, yes).catch(() => false))) return say(t("espace.error"));
      setDecided((d) => ({ ...d, [ref]: yes ? "approved" : "rejected" }));
      say(t(yes ? "espace.card.did.yes" : "espace.card.did.no"));
    },
    ask: (msg) => send(msg, t("espace.ask.sent")),
  };

  // The ⋯ menu: ask Eriphe about the object, or a remark that becomes a rule.
  const ask = (o: Obj, q: string) => {
    setMenu(null);
    send(t("espace.ask.message", titleOf(t, lang, o), q), t("espace.ask.sent"));
  };
  const remember = (key: string, label: string, delta: number, remark?: string) => {
    setRules((rs) => [...rs, { id: crypto.randomUUID(), key, delta, remark, at: new Date().toISOString() }]);
    setMenu(null);
    say(delta > 0 ? t("espace.rule.more", label) : delta <= -5 ? t("espace.rule.never", label) : delta < 0 ? t("espace.rule.less", label) : t("espace.rule.remark"));
  };

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (["INPUT", "TEXTAREA", "SELECT"].includes((e.target as HTMLElement).tagName)) return;
      if (e.key === "Escape") {
        setMenu(null);
        if (tour) stopTour();
        else open(null);
      }
      if (tour && e.key === "ArrowRight") step(1);
      if (tour && e.key === "ArrowLeft") step(-1);
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  });

  const onSubmit = (e: FormEvent<HTMLFormElement>) => {
    e.preventDefault();
    const input = e.currentTarget.elements.namedItem("text") as HTMLInputElement;
    const v = input.value.trim();
    if (!v) return;
    input.value = "";
    if (PRESENT.test(v)) return present();
    send(v, t("espace.sent"));
  };

  const mode = focus ? "side" : comp.mode;
  const ambient = comp.ambient ?? "calm";

  return (
    <MotionConfig reducedMotion="user">
      <div className={`espace ambient-${ambient} mode-${mode} ${wide ? "wide" : ""}`}>
        <aside className="eriphe-panel" aria-live="polite">
          <p className="eriphe-name">{boot.name}</p>
          <div className="eriphe-text">{text && <p>{text}</p>}</div>
          {snap && !snap.sample && placed.length === 0 && shownChips.length === 0 && <p className="eriphe-sample quiet">{t("espace.empty")}</p>}
          {snap?.sample && <p className="eriphe-sample">{t("espace.sample")}</p>}
          <div className="eriphe-actions">
            {tour ? (
              <>
                <button className="btn ghost" onClick={() => step(1)}>
                  {tour.i + 1 < tour.refs.length ? t("espace.next", tour.i + 1, tour.refs.length) : t("espace.finish")}
                </button>
                <button className="btn ghost" onClick={stopTour}>{t("espace.stop")}</button>
              </>
            ) : focus ? (
              <button className="btn ghost" onClick={() => open(null)}>{t("espace.back")}</button>
            ) : placed.length > 1 ? (
              <button className="btn ghost" onClick={present}>{t("espace.present")}</button>
            ) : null}
          </div>
          <form className="eriphe-input" onSubmit={onSubmit}>
            <input name="text" autoComplete="off" placeholder={t("espace.input")} aria-label={t("espace.input")} />
          </form>
          <div className="eriphe-links">
            <a href="/">{t("espace.convo")}</a>
            <button className="linkish" onClick={() => setWide(!wide)} aria-pressed={wide}>{t(wide ? "espace.narrow" : "espace.wide")}</button>
          </div>
        </aside>

        <div className="stage">
          <div className="chips" role="list" aria-label={t("espace.chips")}>
            <AnimatePresence>
              {shownChips.map((c) => (
                <motion.div key={c.obj.ref} layout role="listitem" className={`chip from-${c.from}`}
                  initial={{ opacity: 0, y: -12 }} animate={{ opacity: 1, y: 0 }} exit={{ opacity: 0, y: -12 }} transition={spring}>
                  <button className="chip-open" onClick={() => open(c.obj.ref)}>
                    <span className="chip-text">{lineOf(c.obj)}</span>
                    <span className="chip-from">{[sourceOf(c.obj), t(c.from === "overflow" ? "espace.chip.overflow" : "espace.chip.composition")].filter(Boolean).join(", ")}</span>
                  </button>
                  {c.obj.card?.state === "pending" && <YesNo ref_={c.obj.ref} act={act} />}
                </motion.div>
              ))}
            </AnimatePresence>
          </div>

          <main className="canvas" aria-label={t("espace.canvas")}>
            <LayoutGroup>
              {/* Not mode="popLayout": it injects a <style> element, which the CSP refuses.
                  A module leaving keeps its grid cells while it fades, so nothing jumps. */}
              <AnimatePresence>
                {placed.map((p) => (
                  <motion.article
                    key={p.obj.ref}
                    layout
                    transition={spring}
                    initial={{ opacity: 0, scale: 0.94 }}
                    animate={{ opacity: 1, scale: 1 }}
                    exit={{ opacity: 0, scale: 0.92 }}
                    className={`mod size-${p.size} at-c${p.col} at-r${p.row} w-${p.w} h-${p.h} type-${p.obj.kind}${p.focus ? " focus" : ""}${p.related ? " related" : ""}`}
                    onClick={(e) => {
                      if (p.size === "s" && !p.focus && !(e.target as HTMLElement).closest("button,input,select,a")) open(p.obj.ref);
                    }}
                  >
                    <div className="mod-body">
                      <View o={p.obj} size={p.size} full={p.focus} all={all} act={act} />
                    </div>
                    {p.size !== "s" && <p className="why">{p.why || (p.focus ? t("espace.opened") : p.related ? t("espace.related") : "")}</p>}
                    <button className="more" aria-label={t("espace.menu")} aria-expanded={menu === p.obj.ref}
                      onClick={(e) => { e.stopPropagation(); setMenu(menu === p.obj.ref ? null : p.obj.ref); }}>
                      ⋯
                    </button>
                    {menu === p.obj.ref && <Menu o={p.obj} onAsk={ask} onRule={remember} />}
                  </motion.article>
                ))}
              </AnimatePresence>
            </LayoutGroup>
          </main>
        </div>

        <AnimatePresence>
          {toast && (
            <motion.p key={toast} className="toast" role="status" initial={{ opacity: 0, y: 12 }} animate={{ opacity: 1, y: 0 }} exit={{ opacity: 0 }}>
              {toast}
            </motion.p>
          )}
        </AnimatePresence>
      </div>
    </MotionConfig>
  );
}

// The label of what a rule applies to.
function keyLabel(t: ReturnType<typeof useWords>["t"], key: string): string {
  const [kind, arg] = key.split(/:(.*)/s);
  if (arg) return t("espace.key.mail.domain", arg);
  return t(`espace.key.${kind}`);
}

function Menu({ o, onAsk, onRule }: { o: Obj; onAsk: (o: Obj, q: string) => void; onRule: (key: string, label: string, delta: number, remark?: string) => void }) {
  const { t } = useWords();
  const keys = keysOf(o);
  const [k, setK] = useState(0);
  const [q, setQ] = useState("");
  const [remark, setRemark] = useState("");
  const key = keys[k];
  const label = keyLabel(t, key);
  return (
    <div className="menu" onClick={(e) => e.stopPropagation()}>
      <form onSubmit={(e) => { e.preventDefault(); if (q.trim()) onAsk(o, q.trim()); }}>
        <input name="ask" autoFocus value={q} onChange={(e) => setQ(e.target.value)} placeholder={t("espace.menu.ask")} aria-label={t("espace.menu.ask")} />
      </form>
      {keys.length > 1 && (
        <select name="scope" value={k} onChange={(e) => setK(Number(e.target.value))} aria-label={t("espace.menu")}>
          {keys.map((x, i) => <option key={x} value={i}>{t("espace.menu.scope", keyLabel(t, x))}</option>)}
        </select>
      )}
      <div className="menu-row">
        <button className="btn" onClick={() => onRule(key, label, 1)}>{t("espace.menu.more")}</button>
        <button className="btn" onClick={() => onRule(key, label, -1)}>{t("espace.menu.less")}</button>
        <button className="btn" onClick={() => onRule(key, label, -5)}>{t("espace.menu.never")}</button>
      </div>
      <form onSubmit={(e) => { e.preventDefault(); if (remark.trim()) onRule(key, label, 0, remark.trim()); }}>
        <input name="remark" value={remark} onChange={(e) => setRemark(e.target.value)} placeholder={t("espace.menu.remark")} aria-label={t("espace.menu.remark")} />
      </form>
    </div>
  );
}

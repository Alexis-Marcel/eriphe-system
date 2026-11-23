import { useEffect, useRef, useState, type ClipboardEvent, type DragEvent, type FormEvent } from "react";
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
//
// A message may carry files (attachments.go): picked with the paperclip
// (on a phone, the camera or the photo library), dropped on the bar or
// pasted (a screenshot). They show as chips above the bar until sent,
// each with a ✕; files alone are a message too. eriphe-core keeps them
// and Eriphe reads them (read_attachment).

// ACCEPT is what the picker offers: what eriphe-core takes (attachments.go),
// images, PDFs and texts. A file dropped or pasted that it does not take
// is refused by eriphe-core, in its words.
export const ACCEPT = "image/*,application/pdf,text/plain,text/markdown,text/csv,.txt,.md,.markdown,.csv";

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
  const [files, setFiles] = useState<readonly File[]>([]);
  // note: what the bar says of the files: one too large, too many, or
  // eriphe-core's refusal of one.
  const [note, setNote] = useState("");
  const [dropping, setDropping] = useState(false);
  const depth = useRef(0);
  const field = useRef<HTMLInputElement>(null);
  const picker = useRef<HTMLInputElement>(null);
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
  // attach adds files to the message, as many and as large as
  // eriphe-core takes; the others are said, never dropped silently.
  const attach = (list: readonly File[]) => {
    if (!list.length) return;
    const { many, size } = boot.attach;
    const next = [...files];
    let said = "";
    for (const f of list) {
      if (f.size > size) {
        said = t("espace.bar.file.toobig", f.name, Math.round(size / (1 << 20)));
        continue;
      }
      if (next.length >= many) {
        said = t("espace.bar.file.many", many);
        break;
      }
      next.push(f);
    }
    setFiles(next);
    setNote(said);
    field.current?.focus();
  };
  const say = async (text: string) => {
    const words = text.trim();
    const sending = files;
    if (!words && !sending.length) return;
    setDraft("");
    setFiles([]);
    setFailed(false);
    setNote("");
    if (!boot.live) return;
    step(store, setSent, { text: words || sending.map((f) => f.name).join(", "), after: lastSeq(store.getState().orb), to: talk });
    const r = await api.send(words, talk, sending).catch((): api.Sending => ({ ok: false }));
    if (!r.ok) {
      step(store, setSent, null);
      setDraft(words);
      setFiles(sending);
      if (r.refused) setNote(r.refused);
      else setFailed(true);
    }
  };
  const onPaste = (e: ClipboardEvent<HTMLInputElement>) => {
    const pasted = [...e.clipboardData.files];
    if (!pasted.length) return; // words: pasted as words
    e.preventDefault();
    attach(pasted);
  };
  // Files dragged onto the bar: only files, the graph keeps its own drags.
  const carries = (e: DragEvent) => [...e.dataTransfer.types].includes("Files");
  const drag = {
    onDragEnter: (e: DragEvent) => {
      if (!carries(e)) return;
      depth.current++;
      setDropping(true);
    },
    onDragLeave: (e: DragEvent) => {
      if (!carries(e)) return;
      if (--depth.current <= 0) {
        depth.current = 0;
        setDropping(false);
      }
    },
    onDragOver: (e: DragEvent) => {
      if (carries(e)) e.preventDefault();
    },
    onDrop: (e: DragEvent) => {
      if (!carries(e)) return;
      e.preventDefault();
      depth.current = 0;
      setDropping(false);
      attach([...e.dataTransfer.files]);
    },
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
                {w.text && (
                  <span className="cs-wait-text" title={w.text}>
                    {w.text}
                  </span>
                )}
                {!!w.files?.length && (
                  <span className="cs-wait-files" title={w.files.join(", ")}>
                    <Clip />
                    {w.files.join(", ")}
                  </span>
                )}
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
      {note && (
        <p className="cs-note" role="alert">
          {note}
        </p>
      )}
      {files.length > 0 && (
        <ul className="cs-files" aria-label={t("espace.bar.attached")}>
          {files.map((f, i) => (
            <FileChip key={`${i}:${f.name}:${f.size}`} file={f} remove={t("espace.bar.file.remove", f.name)} onRemove={() => setFiles(files.filter((_, j) => j !== i))} />
          ))}
        </ul>
      )}
      {taken && (
        <p className="cs-note" role="status">
          {t("espace.bar.taken")}
        </p>
      )}

      {/* The picker the paperclip opens: out of the bar, never seen. */}
      <input
        ref={picker}
        type="file"
        className="cs-picker"
        multiple
        accept={ACCEPT}
        hidden
        tabIndex={-1}
        onChange={(e) => {
          attach([...(e.target.files ?? [])]);
          e.target.value = ""; // the same file may be picked again
        }}
      />
      <form className={`cs-bar${dropping ? " dropping" : ""}`} onSubmit={onSubmit} {...drag}>
        <button type="button" className="cs-attach" aria-label={t("espace.bar.attach")} title={t("espace.bar.attach")} onClick={() => picker.current?.click()}>
          <Clip />
        </button>
        <input
          ref={field}
          onPaste={onPaste}
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

// FileChip is a file attached to the message, before it is sent: its
// thumbnail when an image, its name, and a ✕ that takes it off.
function FileChip({ file, remove, onRemove }: { file: File; remove: string; onRemove: () => void }) {
  const image = file.type.startsWith("image/");
  const [src, setSrc] = useState("");
  useEffect(() => {
    if (!image) return;
    const url = URL.createObjectURL(file);
    setSrc(url);
    return () => URL.revokeObjectURL(url);
  }, [file, image]);
  return (
    <li className="cs-file">
      {image && src ? <img src={src} alt="" /> : <FileIcon />}
      <span className="cs-file-name" title={file.name}>
        {file.name}
      </span>
      <button type="button" className="cs-wait-x" aria-label={remove} title={remove} onClick={onRemove}>
        ✕
      </button>
    </li>
  );
}

// Clip is the paperclip: attaching a file, or a message's files.
function Clip() {
  return (
    <svg className="cs-clip" viewBox="0 0 24 24" width="16" height="16" aria-hidden="true">
      <path d="M20 11.5l-8.1 8.1a5 5 0 01-7.1-7.1l8.5-8.5a3.3 3.3 0 014.7 4.7l-8.5 8.5a1.7 1.7 0 01-2.4-2.4l7.8-7.8" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" strokeLinejoin="round" />
    </svg>
  );
}

// FileIcon stands for a file that is not an image.
function FileIcon() {
  return (
    <svg className="cs-file-icon" viewBox="0 0 24 24" width="16" height="16" aria-hidden="true">
      <path d="M14 3H7a2 2 0 00-2 2v14a2 2 0 002 2h10a2 2 0 002-2V8zM14 3v5h5" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinejoin="round" />
    </svg>
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

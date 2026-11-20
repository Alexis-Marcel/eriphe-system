import { useEffect, useRef, useState, type KeyboardEvent as KeyEv } from "react";
import { createPortal } from "react-dom";
import { stamp } from "../a2ui/format";
import { label, type PageVersion } from "../data/api";
import { useWords } from "../i18n";

const LIST_W = 230; // the list's width (.pg-vlist, espace.css)

// Versions is a page's versions folded into one small chip in its head:
// the version shown (« v17 »), marked when it is not the last. A click
// opens a short list under it, the newest first, each with who made it
// and when, scrolling when there are many; choosing one shows it. Escape,
// a click outside or Tab closes it; the arrows, Home and End move in it.
// Drawn only when there is more than one version. eriphe-core lists the
// versions as the user counts them (nodePageOf, web_graph.go): code's own
// writes are left out but the last, so a list may skip numbers (« v98,
// v1 »: the last a refresh of its sources, the first the user's).
export function Versions({ versions, shown, last, shift = 0, onPick }: { versions: PageVersion[]; shown: number; last: number; shift?: number; onPick: (n: number) => void }) {
  const { t, lang } = useWords();
  const [open, setOpen] = useState<{ top: number; left: number } | null>(null);
  const chip = useRef<HTMLButtonElement>(null);
  const list = useRef<HTMLDivElement>(null);
  const close = (focus: boolean) => {
    setOpen(null);
    if (focus) chip.current?.focus();
  };
  const toggle = () => {
    const r = chip.current?.getBoundingClientRect();
    // Under it, its right edge on the chip's, kept on the screen.
    const w = Math.min(LIST_W, innerWidth - 16);
    setOpen(open || !r ? null : { top: r.bottom + 4, left: Math.max(8, Math.min(r.right - w, innerWidth - w - 8)) });
  };
  useEffect(() => {
    if (!open) return;
    // The version shown takes the focus, in view.
    const on = list.current?.querySelector<HTMLElement>("[aria-selected='true']") ?? list.current?.querySelector<HTMLElement>("[role='option']");
    on?.focus();
    on?.scrollIntoView?.({ block: "nearest" });
    const away = (e: Event) => !chip.current?.contains(e.target as Node) && !list.current?.contains(e.target as Node) && setOpen(null);
    document.addEventListener("pointerdown", away);
    return () => document.removeEventListener("pointerdown", away);
  }, [open]);
  if (versions.length < 2) return null;
  const old = shown !== last;
  const newest = [...versions].reverse();
  // Who made it: the user, a file changed outside, else Eriphe (its brain,
  // a task, its code).
  const who = (author: string) => t(author === "user" || author === "outside" ? `espace.who.${author}` : "espace.who.brain");
  const keys = (e: KeyEv) => {
    const all = [...(list.current?.querySelectorAll<HTMLElement>("[role='option']") ?? [])];
    const i = all.indexOf(document.activeElement as HTMLElement);
    const go = (j: number) => {
      e.preventDefault();
      all[Math.max(0, Math.min(all.length - 1, j))]?.focus();
    };
    if (e.key === "ArrowDown") go(i + 1);
    else if (e.key === "ArrowUp") go(i - 1);
    else if (e.key === "Home") go(0);
    else if (e.key === "End") go(all.length - 1);
    else if (e.key === "Escape") {
      e.preventDefault();
      e.stopPropagation();
      close(true);
    } else if (e.key === "Tab") close(false);
  };
  const why = old ? t("espace.page.v.old", label(shown, shift), label(last, shift)) : t("espace.page.v.chip", label(shown, shift));
  return (
    <>
      <button
        ref={chip}
        type="button"
        className={`pg-vchip${old ? " old" : ""}`}
        aria-haspopup="listbox"
        aria-expanded={!!open}
        aria-label={why}
        title={why}
        onClick={toggle}
        onKeyDown={(e) => {
          if (e.key === "ArrowDown" && !open) {
            e.preventDefault();
            toggle();
          }
        }}
      >
        v{label(shown, shift)}
        <svg viewBox="0 0 10 10" width="8" height="8" aria-hidden="true">
          <path d="M2 3.5 5 6.5 8 3.5" fill="none" stroke="currentColor" strokeWidth="1.4" strokeLinecap="round" strokeLinejoin="round" />
        </svg>
      </button>
      {open &&
        createPortal(
          <div
            className="pg-vlist"
            role="listbox"
            aria-label={t("espace.page.versions")}
            ref={list}
            style={{ top: open.top, left: open.left }}
            onKeyDown={keys}
            onPointerDown={(e) => e.stopPropagation()}
          >
            {newest.map((v) => (
              <button
                key={v.n}
                type="button"
                role="option"
                aria-selected={v.n === shown}
                tabIndex={v.n === shown ? 0 : -1}
                onClick={() => {
                  onPick(v.n);
                  close(true);
                }}
              >
                <b>v{label(v.n, shift)}</b>
                <span className="pg-vwho">{who(v.author)}</span>
                <time dateTime={v.at}>{stamp(lang, v.at)}</time>
                {v.n === last && <em>{t("espace.page.v.last")}</em>}
              </button>
            ))}
          </div>,
          document.body,
        )}
    </>
  );
}

import { Markdown } from "../a2ui/markdown";
import * as api from "../data/api";
import { openPage, step, store, useGraph } from "../data/store";
import { titleOf } from "../graph/model";
import { useWords } from "../i18n";
import type { Show } from "./model";

// Words are what the orb says, in its bubble (Orb.tsx) or the phone's line
// (Line.tsx): Eriphe's few lines, marked when written after reading
// external content, never replaced; the home's answer whole, as Markdown;
// that it works; that it could not answer, and the way to try again; the
// card it points at; a subject that began to wait. « Voir plus » and
// « Voir » open the subject's page, where the rest is; ✕ closes what
// stays until the user moves on.
export function Words({ show, dismiss, open }: { show: Show; dismiss: () => void; open: (id: string) => void }) {
  const { t } = useWords();
  const nodes = useGraph((s) => s.nodes);
  const ext = <span className="orb-ext" role="img" title={t("espace.orb.external")} aria-label={t("espace.orb.external")} />;
  const x = <Close onClick={dismiss} label={t("espace.orb.close")} />;
  switch (show.mode) {
    case "work":
      return show.draft ? (
        <p className="orb-text draft">{show.draft}</p>
      ) : (
        <p className="orb-dots" aria-hidden="true">
          <i />
          <i />
          <i />
        </p>
      );
    case "failed":
      return (
        <>
          <p className="orb-text orb-error" role="alert">
            {t("espace.orb.failed")}{" "}
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
            <a href="/settings/agents">{t("espace.orb.account")}</a>
          </p>
          {x}
        </>
      );
    case "wait":
      return (
        <>
          <p className="orb-text">
            {show.words ? show.words.text : t("espace.orb.wait")}
            {show.words?.external && ext}{" "}
            <button type="button" className="orb-link" onClick={() => pointCard(show.node!, open)}>
              {t("espace.orb.show")}
            </button>
          </p>
          {x}
        </>
      );
    case "answer":
    case "said":
      if (show.call) {
        return (
          <>
            <p className="orb-text">
              {t("espace.orb.waits", titleOf(nodes[show.call], t("espace.external")))}{" "}
              <button type="button" className="orb-link" onClick={() => open(show.call!)}>
                {t("espace.orb.show")}
              </button>
            </p>
            {x}
          </>
        );
      }
      if (!show.words) return null;
      if (show.words.whole) {
        return (
          <>
            <div className="orb-answer">
              <Markdown text={show.words.text} newTab={t("espace.a2.newtab")} />
              {show.words.external && ext}
            </div>
            {x}
          </>
        );
      }
      return (
        <>
          <p className="orb-text">
            {show.words.text}
            {show.words.external && ext}
            {show.words.more && show.node && (
              <>
                {" "}
                <button type="button" className="orb-link" onClick={() => open(show.node!)}>
                  {t("espace.orb.more")}
                </button>
              </>
            )}
          </p>
          {x}
        </>
      );
  }
  return null;
}

function Close({ onClick, label }: { onClick: () => void; label: string }) {
  return (
    <button type="button" className="orb-x" aria-label={label} title={label} onClick={onClick}>
      ✕
    </button>
  );
}

// pointCard opens the page that holds the card that waits, and brings the
// card into view once it is drawn.
export function pointCard(id: string, open: (id: string) => void) {
  if (!store.getState().open.includes(id)) open(id);
  else step(store, openPage, id, true);
  let n = 0;
  const find = () => {
    const card = document.querySelector<HTMLElement>(`[data-page="${CSS.escape(id)}"] .a2-proposal-pending, .ls-page .a2-proposal-pending`);
    if (card) card.scrollIntoView({ block: "center", behavior: matchMedia("(prefers-reduced-motion: reduce)").matches ? "auto" : "smooth" });
    else if (n++ < 20) setTimeout(find, 150);
  };
  setTimeout(find, 100);
}

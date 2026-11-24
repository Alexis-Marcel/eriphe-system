import { Markdown } from "../a2ui/markdown";
import * as api from "../data/api";
import { useWords } from "../i18n";
import type { Show } from "./model";

// Words are what the orb says, in its bubble (Orb.tsx) or the phone's line
// (Line.tsx): Eriphe's few lines, marked when written after reading
// external content, never replaced; the home's answer whole, as Markdown;
// that it works; that it could not answer, and the way to try again.
// « Voir plus » unfolds the whole answer in place, as Markdown; ✕ closes
// what stays until the user moves on.
export function Words({ show, dismiss, unfold }: { show: Show; dismiss: () => void; unfold: () => void }) {
  const { t } = useWords();
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
    case "answer":
      if (!show.words) return null;
      if (show.words.whole || show.full) {
        return (
          <>
            <div className="orb-answer">
              <Markdown text={show.full ? show.words.full! : show.words.text} newTab={t("espace.a2.newtab")} />
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
            {show.words.more && show.words.full && (
              <>
                {" "}
                <button type="button" className="orb-link" onClick={unfold}>
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

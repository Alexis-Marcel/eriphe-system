import { useEffect, useState } from "react";
import { saveFile, type NodeFile } from "../data/api";
import { useWords } from "../i18n";
import Editor from "./Editor";
import { dirty, fromFile, keepMine, read, saved, saving, takeNewer, typed, type Edit } from "./edit";


// MdTab is a node's text as the user edits it (#633): its body, without
// code's fields, in an editor; saved on the version it was read at (edit.ts).
// An older version shows as it was, not to be written; so does the dev
// server's sample.
export function MdTab({ id, file, latest, live }: { id: string; file: NodeFile; latest: boolean; live: boolean }) {
  const { t } = useWords();
  const [e, setE] = useState<Edit>(() => fromFile(file));
  useEffect(() => setE((x) => (latest ? read(x, file) : fromFile(file))), [file, latest]);
  const writable = latest && live;

  const save = () => {
    if (!writable || !dirty(e) || e.status === "saving") return;
    const text = e.text;
    setE(saving);
    saveFile(id, text, e.base.hash).then(
      (r) => setE((x) => saved(x, text, r)),
      () => setE((x) => saved(x, text, { ok: false, why: "" })),
    );
  };

  return (
    <div className="pg-md">
      <Editor value={e.text} readOnly={!writable} label={t("espace.md.label")} onChange={(text) => setE((x) => typed(x, text))} onSave={save} />
      {writable && (
        <div className="pg-md-bar">
          <span className="pg-md-state" role="status">
            {e.status === "saving" ? t("espace.md.saving") : e.status === "saved" && !dirty(e) ? t("espace.md.saved", e.base.version) : e.status === "failed" ? t("espace.md.failed") : ""}
          </span>
          <button type="button" className="btn" disabled={!dirty(e) || e.status === "saving" || e.status === "conflict"} onClick={save}>
            {t("espace.md.save")}
          </button>
        </div>
      )}
      {e.status === "conflict" && e.newer && (
        <div className="pg-md-conflict" role="alert">
          <p>{t("espace.md.conflict", e.newer.version)}</p>
          <pre className="pg-md-text">{e.newer.body}</pre>
          <p className="pg-md-choice">
            <button type="button" className="btn" onClick={() => setE(takeNewer)}>
              {t("espace.md.take", e.newer.version)}
            </button>
            <button type="button" className="btn" onClick={() => setE(keepMine)}>
              {t("espace.md.keep")}
            </button>
          </p>
        </div>
      )}
    </div>
  );
}

import { useEffect, useRef, useState } from "react";
import { useWords } from "../i18n";

// PdfView is a node's PDF in its page: its pages one under the other, the
// width of the page, under a link that downloads it. The bytes come from
// eriphe-core's signed address of the node's file (web_media.go), which
// still serves it as a download: the page draws it itself (pdf.ts, loaded
// only now), so nothing of the PDF ever runs in the workspace's origin.
// Should it not draw (a damaged file), the link stays.
export function PdfView({ src, name }: { src: string; name: string }) {
  const { t } = useWords();
  const box = useRef<HTMLDivElement>(null);
  const [state, setState] = useState<"loading" | "shown" | "failed">("loading");
  useEffect(() => {
    let gone = false;
    let stop = () => {};
    setState("loading");
    (async () => {
      const res = await fetch(src, { credentials: "same-origin" });
      if (!res.ok) throw new Error(`${res.status}`);
      const data = await res.arrayBuffer();
      const { showPdf } = await import("./pdf");
      if (gone || !box.current) return;
      const end = await showPdf(data, box.current);
      if (gone) return end();
      stop = end;
      setState("shown");
    })().catch(() => !gone && setState("failed"));
    return () => {
      gone = true;
      stop();
      box.current?.replaceChildren();
    };
  }, [src]);
  return (
    <figure className="pg-pdf" data-state={state}>
      <figcaption>
        <a className="pg-file" href={src} download={`${name}.pdf`}>
          {name} · {t("espace.props.file")}
        </a>
      </figcaption>
      <div ref={box} className="pg-pdf-pages" aria-label={name} hidden={state === "failed"} />
    </figure>
  );
}

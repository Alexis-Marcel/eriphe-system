import { useEffect, useRef, useState } from "react";
import type { Mounted } from "./cm";

// Editor is the .md tab's text editor: CodeMirror (cm.ts), loaded the
// first time one shows; until then, the text as it is, not to be written.
export default function Editor(p: { value: string; readOnly: boolean; label: string; onChange: (text: string) => void; onSave: () => void }) {
  const host = useRef<HTMLDivElement>(null);
  const view = useRef<Mounted | null>(null);
  const cb = useRef(p);
  cb.current = p;
  const [ready, setReady] = useState(false);

  useEffect(() => {
    let gone = false;
    void import("./cm").then(({ mount }) => {
      if (gone || !host.current) return;
      view.current = mount(host.current, cb.current.value, () => cb.current);
      setReady(true);
    });
    return () => {
      gone = true;
      view.current?.destroy();
      view.current = null;
    };
  }, []);

  useEffect(() => view.current?.set(p.value), [p.value]);
  useEffect(() => view.current?.readOnly(p.readOnly), [p.readOnly]);

  return (
    <div className="pg-editor" ref={host}>
      {!ready && <pre className="pg-md-text">{p.value}</pre>}
    </div>
  );
}

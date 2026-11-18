import { useEffect, useRef } from "react";
import { defaultKeymap, history, historyKeymap } from "@codemirror/commands";
import { Compartment, EditorState } from "@codemirror/state";
import { EditorView, keymap } from "@codemirror/view";

// Editor is the .md tab's text editor, CodeMirror's: the text as it is,
// its history, ⌘S or Ctrl-S to save. CodeMirror styles itself with a
// <style> element in a document, which the page's Content-Security-Policy
// (style-src 'self') refuses, and with a constructed stylesheet in a shadow
// root: so it lives in one, its look the page's through the variables,
// which cross into it. In the one bundle: a chunk of its own would load the
// entry again without its ?v=, a second React.
const ro = new Compartment(); // whether it may be written

const look = EditorView.theme({
  "&": { color: "var(--fg)", backgroundColor: "transparent", fontFamily: "var(--mono)", fontSize: "12px", borderRadius: "10px" },
  "&.cm-focused": { outline: "2px solid color-mix(in oklab, var(--accent) 45%, transparent)", outlineOffset: "1px" },
  ".cm-scroller": { fontFamily: "var(--mono)", lineHeight: "1.55" },
  ".cm-content": { padding: "12px 14px", caretColor: "var(--fg)" },
  ".cm-line": { padding: "0" },
  ".cm-cursor, .cm-dropCursor": { borderLeftColor: "var(--fg)" },
  "&.cm-focused > .cm-scroller > .cm-selectionLayer .cm-selectionBackground, .cm-selectionBackground": {
    backgroundColor: "color-mix(in oklab, var(--accent) 22%, transparent)",
  },
});

export default function Editor(p: { value: string; readOnly: boolean; label: string; onChange: (text: string) => void; onSave: () => void }) {
  const host = useRef<HTMLDivElement>(null);
  const view = useRef<EditorView | null>(null);
  const cb = useRef(p);
  cb.current = p;

  useEffect(() => {
    const el = host.current!;
    const shadow = el.shadowRoot ?? el.attachShadow({ mode: "open" });
    const parent = document.createElement("div");
    shadow.replaceChildren(parent);
    const v = new EditorView({
      root: shadow,
      parent,
      state: EditorState.create({
        doc: cb.current.value,
        extensions: [
          history(),
          look,
          EditorView.lineWrapping,
          EditorView.contentAttributes.of({ "aria-label": cb.current.label }),
          keymap.of([{ key: "Mod-s", preventDefault: true, run: () => (cb.current.onSave(), true) }, ...defaultKeymap, ...historyKeymap]),
          EditorView.updateListener.of((u) => u.docChanged && cb.current.onChange(u.state.doc.toString())),
          ro.of(EditorState.readOnly.of(cb.current.readOnly)),
        ],
      }),
    });
    view.current = v;
    return () => v.destroy();
  }, []);

  // A text from outside (another version, the newer one taken) replaces
  // the editor's; the user's own typing comes back the same and is left.
  useEffect(() => {
    const v = view.current;
    if (!v || v.state.doc.toString() === p.value) return;
    v.dispatch({ changes: { from: 0, to: v.state.doc.length, insert: p.value } });
  }, [p.value]);

  useEffect(() => {
    view.current?.dispatch({ effects: ro.reconfigure(EditorState.readOnly.of(p.readOnly)) });
  }, [p.readOnly]);

  return <div className="pg-editor" ref={host} />;
}

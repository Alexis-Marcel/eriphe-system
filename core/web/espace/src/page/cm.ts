import { defaultKeymap, history, historyKeymap } from "@codemirror/commands";
import { Compartment, EditorState } from "@codemirror/state";
import { EditorView, keymap } from "@codemirror/view";

// cm is CodeMirror, the .md tab's editor, in a chunk of its own: loaded
// the first time a .md tab opens (Editor.tsx), never with the app. It
// imports nothing of the app's (no React either): a chunk that did would
// load the entry again without its ?v=, a second app. Its text as it is,
// its history, ⌘S or Ctrl-S to save. CodeMirror styles itself with a
// <style> element in a document, which the page's Content-Security-Policy
// (style-src 'self') refuses, and with a constructed stylesheet in a shadow
// root: so it lives in one, its look the page's through the variables,
// which cross into it.

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

export type Hooks = { label: string; readOnly: boolean; onChange(text: string): void; onSave(): void };

// An editor mounted: its text set from outside, whether it may be
// written, and gone.
export type Mounted = { set(text: string): void; readOnly(ro: boolean): void; destroy(): void };

export function mount(el: HTMLElement, text: string, hooks: () => Hooks): Mounted {
  const shadow = el.shadowRoot ?? el.attachShadow({ mode: "open" });
  const parent = document.createElement("div");
  shadow.replaceChildren(parent);
  const ro = new Compartment();
  const h = hooks();
  const v = new EditorView({
    root: shadow,
    parent,
    state: EditorState.create({
      doc: text,
      extensions: [
        history(),
        look,
        EditorView.lineWrapping,
        EditorView.contentAttributes.of({ "aria-label": h.label }),
        keymap.of([{ key: "Mod-s", preventDefault: true, run: () => (hooks().onSave(), true) }, ...defaultKeymap, ...historyKeymap]),
        EditorView.updateListener.of((u) => u.docChanged && hooks().onChange(u.state.doc.toString())),
        ro.of(EditorState.readOnly.of(h.readOnly)),
      ],
    }),
  });
  return {
    // A text from outside (another version, the newer one taken) replaces
    // the editor's; the user's own typing comes back the same and is left.
    set: (t) => {
      if (v.state.doc.toString() !== t) v.dispatch({ changes: { from: 0, to: v.state.doc.length, insert: t } });
    },
    readOnly: (r) => v.dispatch({ effects: ro.reconfigure(EditorState.readOnly.of(r)) }),
    destroy: () => v.destroy(),
  };
}

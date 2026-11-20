import type { GraphNode } from "../data/api";
import { useWords } from "../i18n";

// A title written after an external read (titleExternal) may be someone
// else's words: it is shown as it is, never replaced, and always marked
// so, in the external amber with a small ⚠ whose words say why (a
// tooltip, and a screen reader's). A trusted title has no mark.

// ExtMark is the small ⚠ after such a title.
export function ExtMark() {
  const { t } = useWords();
  const why = t("espace.title.external");
  return (
    <span className="ext-mark" role="img" aria-label={why} title={why}>
      ⚠
    </span>
  );
}

// NodeTitle is a node's title as the screen shows it: marked when it may
// be someone else's words. A title cut short loses its end, never its mark.
export function NodeTitle({ x, fallback }: { x: GraphNode | undefined; fallback: string }) {
  const title = x?.title || fallback;
  if (!x?.titleExternal) return <>{title}</>;
  return (
    <span className="title-ext">
      <span className="title-tx">{title}</span>
      <ExtMark />
    </span>
  );
}

// UnaskedMark is the sign of a node the user did not ask for: a skill or
// an event opened it. Their own nodes have none. A small ↯ after its
// title, on its bubble and in its page's header (there, a small badge
// beside « extérieur »); its words say why (a tooltip, and a screen
// reader's), its page's ⋯ menu who opened it.
export function UnaskedMark() {
  const { t } = useWords();
  const why = t("espace.unasked.why");
  return (
    <span className="unasked-mark" role="img" aria-label={why} title={why}>
      ↯
    </span>
  );
}

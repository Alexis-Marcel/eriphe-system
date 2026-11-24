import type { GraphNode } from "../data/api";
import { useWords } from "../i18n";

// Someone else's words have one sign, everywhere: a small ⚠ whose words
// say why (a tooltip, and a screen reader's). A title written after an
// external read (titleExternal) may be such words: it is shown as it is,
// never replaced, in the external amber, with its ⚠. A page's head says
// it once for the whole node (Page.tsx): one ⚠, never a second sign.

// ExtMark is the small ⚠: by default, after a title that may be someone
// else's words; why, for what else it marks.
export function ExtMark({ why }: { why?: string }) {
  const { t } = useWords();
  const words = why ?? t("espace.title.external");
  return (
    <span className="ext-mark" role="img" aria-label={words} title={words}>
      ⚠
    </span>
  );
}

// NodeTitle is a node's title as the screen shows it: marked when it may
// be someone else's words (mark: false when the ⚠ is said beside it, once
// for the node). A title cut short loses its end, never its mark.
export function NodeTitle({ x, fallback, mark = true }: { x: GraphNode | undefined; fallback: string; mark?: boolean }) {
  const title = x?.title || fallback;
  if (!x?.titleExternal) return <>{title}</>;
  return (
    <span className="title-ext">
      <span className="title-tx">{title}</span>
      {mark && <ExtMark />}
    </span>
  );
}

// MaintainedMark is the sign of a node a skill maintains, the same words
// everywhere: ∞ beside its title (its page, its row on a phone); on its
// bubble, the satellite that orbits it, whose words are its core's
// tooltip. by: the skills that maintain it.
export function MaintainedMark({ by }: { by: string[] }) {
  const { t } = useWords();
  const why = t("espace.maintained", by.join(", "));
  return (
    <span className="pg-maintained" role="img" aria-label={why} title={why}>
      ∞
    </span>
  );
}

// UnaskedMark is the sign of a node the user did not ask for: a skill or
// an event opened it. Their own nodes have none. A small ↯ after its
// title, on its bubble and in its page's header (there, beside its ⚠ if
// it has one); its words say why (a tooltip, and a screen reader's), its
// page's ⋯ menu who opened it.
export function UnaskedMark() {
  const { t } = useWords();
  const why = t("espace.unasked.why");
  return (
    <span className="unasked-mark" role="img" aria-label={why} title={why}>
      ↯
    </span>
  );
}

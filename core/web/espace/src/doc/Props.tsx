import type { NodePage, PageProp } from "../data/api";
import { number, num, wall, when } from "../a2ui/format";
import { useWords, type Lang } from "../i18n";

// Props shows a node's tags and properties, as its version says them,
// read-only, above its text: its tags as #tags, then each property, its
// name and its value (a number, a date, a list written in the user's
// language). Nothing when it has none.
export function Props({ page }: { page: NodePage }) {
  const { t, lang } = useWords();
  const tags = page.tags ?? [];
  const props = page.props ?? [];
  if (!tags.length && !props.length) return null;
  return (
    <dl className="pg-props" aria-label={t("espace.props")}>
      {tags.length > 0 && (
        <div className="pg-tags">
          {tags.map((tag) => (
            <span key={tag} className="pg-tag">
              #{tag}
            </span>
          ))}
        </div>
      )}
      {props.map((p) => (
        <div key={p.key} className="pg-prop">
          <dt>{p.key}</dt>
          <dd>{shown(lang, p)}</dd>
        </div>
      ))}
    </dl>
  );
}

// shown is a property's value as the page writes it.
export function shown(lang: Lang, p: PageProp): string {
  const one = (v: unknown): string => {
    if (v === null || v === undefined || typeof v === "object") return "";
    if (p.kind === "number") {
      const n = num(v);
      if (n !== undefined) return number(lang, n);
    }
    if (p.kind === "date") {
      const w = wall(v);
      if (w) return when(lang, w);
    }
    return String(v);
  };
  return Array.isArray(p.value) ? p.value.map(one).filter(Boolean).join(", ") : one(p.value);
}

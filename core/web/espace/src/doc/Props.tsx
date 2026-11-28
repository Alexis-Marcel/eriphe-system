import type { NodePage, PageProp } from "../data/api";
import { number, num, wall, when } from "../a2ui/format";
import { useWords, type Lang } from "../i18n";
import { PdfView } from "./PdfView";

// Props shows a node's tags and properties, as its version says them,
// read-only, above its text: its pictures first (a property that names
// one of its files: a photo kept with it), then its PDFs, each drawn
// whole with a link to download it (PdfView), then its tags as #tags,
// then each other property, its name and its value (a number, a date, a list written in the user's language).
// Nothing when it has none.
export function Props({ page }: { page: NodePage }) {
  const tags = page.tags ?? [];
  const media = (page.props ?? []).filter((p) => p.media?.length);
  const props = (page.props ?? []).filter((p) => !p.media?.length);
  if (!tags.length && !props.length && !media.length) return null;
  const files = media.flatMap((p) => (p.media ?? []).map((m, i) => ({ ...m, name: p.key, key: `${p.key}-${i}` })));
  const pictures = files.filter((m) => m.image);
  const pdfs = files.filter((m) => !m.image);
  return (
    <>
      {pictures.length > 0 && (
        <div className="pg-media">
          {pictures.map((m) => (
            <img key={m.key} className="pg-picture" src={m.src} alt={m.name} loading="lazy" />
          ))}
        </div>
      )}
      {pdfs.map((m) => (
        <PdfView key={m.key} src={m.src} name={m.name} />
      ))}
      {(tags.length > 0 || props.length > 0) && <PropList tags={tags} props={props} />}
    </>
  );
}

function PropList({ tags, props }: { tags: string[]; props: PageProp[] }) {
  const { t, lang } = useWords();
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

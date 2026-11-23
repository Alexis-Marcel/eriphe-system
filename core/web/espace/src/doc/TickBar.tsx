import { runAction, tickedRows, useTicks } from "../a2ui/ticks";
import type { SourceList } from "../data/api";
import { useWords } from "../i18n";

// Prototype « Eriphe coche, tu cliques » (a2ui/ticks.ts): the page's bar
// while rows Eriphe ticked wait. One button per action, with how many rows
// it takes and over how many accounts (« Corbeille · 23 · 2 comptes »):
// one click is the yes for all of them, one call per account behind it.
// Done, it says so in one line (« 21 à la corbeille »). It sticks to the
// bottom of the page while the page scrolls.
export function TickBar({ node, lists }: { node: string; lists: SourceList[] }) {
  const { t } = useWords();
  const ticks = useTicks(node);
  const { rows, actions } = tickedRows(lists, ticks);
  const names = Object.keys(actions);
  const counts = names
    .map((a) => {
      const going = rows.filter((r) => r.action === a && !r.left);
      const accounts = new Map<string, number>();
      for (const r of going) accounts.set(r.account, (accounts.get(r.account) ?? 0) + 1);
      return { a, n: going.length, accounts };
    })
    .filter((c) => c.n > 0);
  const left = rows.filter((r) => r.left).length;
  const result = ticks.result;
  if (!counts.length && !result) return null;
  return (
    <div className="tk-bar" role="region" aria-label={t("espace.tick.bar")}>
      {result && (
        <p className={`tk-result${result.n < 0 ? " failed" : ""}`} role="status" key={result.text}>
          {result.n < 0 ? t("espace.tick.failed") : result.text}
        </p>
      )}
      {left > 0 && <span className="tk-left">{t("espace.tick.left", left)}</span>}
      <span className="tk-acts">
        {counts.map(({ a, n, accounts }) => {
          const per = [...accounts].map(([acc, k]) => `${acc} : ${k}`).join(" · ");
          return (
            <button
              key={a}
              type="button"
              className={`btn a2-button tk-go tk-a-${a}${a === counts[0].a ? " a2-button-primary" : ""}${ticks.busy === a ? " is-busy" : ""}`}
              disabled={!!ticks.busy}
              aria-busy={ticks.busy === a}
              title={t("espace.tick.calls", per)}
              onClick={() => runAction(node, a, rows, actions[a])}
            >
              <span>{actions[a].label}</span>
              <span className="tk-n">{n}</span>
              {accounts.size > 1 && <span className="tk-acc">{t("espace.tick.accounts", accounts.size)}</span>}
            </button>
          );
        })}
      </span>
    </div>
  );
}

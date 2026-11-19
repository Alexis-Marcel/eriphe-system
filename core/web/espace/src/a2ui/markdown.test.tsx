// @vitest-environment jsdom
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { cells, Markdown } from "./markdown";

// draw renders Markdown and gives its root, to read as the page shows it.
function draw(text: string): HTMLElement {
  const el = document.createElement("div");
  el.innerHTML = renderToStaticMarkup(<Markdown text={text} newTab="(new tab)" />);
  return el.querySelector(".a2-markdown")!;
}

const texts = (els: Iterable<Element>) => [...els].map((e) => e.textContent);

// A table as the brain writes one: PromQL in code spans, with _, *, {},
// quotes, and pipes escaped (\|) inside the code.
const ALERTS = [
  "Ce que je propose de surveiller :",
  "",
  "| Signal | PromQL | Valeur maintenant | Seuil proposé | Pourquoi |",
  "|---|---|:---:|---:|:---|",
  '| Cible tombée | `up{job="node_exporter", environment="prod"} == 0` | 0 | > 0 pendant **5 min** | un *exporter* muet |',
  "| Latence p95 | `histogram_quantile(0.95, sum by (le) (rate(http_request_duration_seconds_bucket[30m])))` | 0,42 s | > 1 s | la page `/api` ralentit |",
  "| Erreurs | `sum(rate(http_requests_total{code=~\"5..\"}[5m])) / sum(rate(http_requests_total[5m])) * 100` | 0,3 % | > 2 % | _tout_ le reste |",
  "| Disque | `node_filesystem_avail_bytes{mountpoint=~\"/\\|/data\"} / node_filesystem_size_bytes` | 61 % | < 10 % | __name__ reste littéral |",
  "| Séries | `count({__name__=~\"job_.*\"})` | 1 204 | — | snake_case_name |",
  "",
  "Après le tableau, du texte normal avec `du_code` et *une emphase*.",
].join("\n");

describe("a table", () => {
  it("is drawn as a table, its header, alignment and rows", () => {
    const md = draw(ALERTS);
    const table = md.querySelector(".a2-md-table > table")!;
    expect(table).not.toBeNull();
    expect(texts(table.querySelectorAll("thead th"))).toEqual(["Signal", "PromQL", "Valeur maintenant", "Seuil proposé", "Pourquoi"]);
    const rows = [...table.querySelectorAll("tbody tr")];
    expect(rows).toHaveLength(5);
    for (const r of rows) expect(r.querySelectorAll("td")).toHaveLength(5);
    const ths = [...table.querySelectorAll("th")];
    expect(ths.map((t) => t.className)).toEqual(["", "", "a2-md-center", "a2-md-right", "a2-md-left"]);
    expect(rows[0].querySelectorAll("td")[3].className).toBe("a2-md-right");
    // The text before and after is not in the table.
    expect(md.querySelector("p")?.textContent).toBe("Ce que je propose de surveiller :");
    expect(md.lastElementChild?.tagName).toBe("P");
  });

  it("keeps its code literal, escaped pipes as pipes", () => {
    const md = draw(ALERTS);
    const code = texts(md.querySelectorAll("tbody td:nth-child(2) code"));
    expect(code).toEqual([
      'up{job="node_exporter", environment="prod"} == 0',
      "histogram_quantile(0.95, sum by (le) (rate(http_request_duration_seconds_bucket[30m])))",
      'sum(rate(http_requests_total{code=~"5.."}[5m])) / sum(rate(http_requests_total[5m])) * 100',
      'node_filesystem_avail_bytes{mountpoint=~"/|/data"} / node_filesystem_size_bytes',
      'count({__name__=~"job_.*"})',
    ]);
    // Nothing is read inside code, and code holds no more than itself.
    expect(md.querySelectorAll("code em, code strong, code a, code code")).toHaveLength(0);
    for (const td of md.querySelectorAll("tbody td:nth-child(2)")) expect(td.childNodes).toHaveLength(1);
  });

  it("reads the cells' Markdown, and leaves words with underscores alone", () => {
    const md = draw(ALERTS);
    const why = [...md.querySelectorAll("tbody tr")].map((r) => r.querySelectorAll("td")[4]);
    expect(why[0].querySelector("em")?.textContent).toBe("exporter");
    expect(md.querySelector("tbody tr td:nth-child(4) strong")?.textContent).toBe("5 min");
    expect(why[1].querySelector("code")?.textContent).toBe("/api");
    expect(why[2].querySelector("em")?.textContent).toBe("tout");
    // __name__ is strong in CommonMark (it stands alone); snake_case_name is a word.
    expect(why[4].textContent).toBe("snake_case_name");
    expect(why[4].querySelector("em, strong")).toBeNull();
    // After the table, the page is text again, not code.
    const last = md.lastElementChild!;
    expect(last.textContent).toBe("Après le tableau, du texte normal avec du_code et une emphase.");
    expect(last.querySelector("code")?.textContent).toBe("du_code");
    expect(last.querySelector("em")?.textContent).toBe("une emphase");
    expect(last.closest("pre, code")).toBeNull();
  });

  it("splits its rows as GFM does", () => {
    expect(cells("| a | b |")).toEqual(["a", "b"]);
    expect(cells("a | b")).toEqual(["a", "b"]);
    expect(cells("| a \\| b | `c \\| d` |")).toEqual(["a | b", "`c | d`"]);
    // An unescaped pipe splits the row, inside a code span too.
    expect(cells("| `a | b` |")).toEqual(["`a", "b`"]);
    expect(cells("| a | |")).toEqual(["a", ""]);
  });

  it("fits short and long rows to its header, and ends on a blank line", () => {
    const md = draw("| a | b |\n|---|---|\n| 1 |\n| 1 | 2 | 3 |\n\nfin");
    const rows = [...md.querySelectorAll("tbody tr")].map((r) => texts(r.querySelectorAll("td")));
    expect(rows).toEqual([["1", ""], ["1", "2"]]);
    expect(md.lastElementChild?.outerHTML).toBe("<p>fin</p>");
  });

  it("interrupts a paragraph, and needs its delimiter row", () => {
    const md = draw("Voici :\n| a | b |\n|---|---|\n| 1 | 2 |");
    expect(md.firstElementChild?.outerHTML).toBe("<p>Voici :</p>");
    expect(md.querySelector("table")).not.toBeNull();
    expect(draw("| a | b |\n| c | d |").querySelector("table")).toBeNull();
    // Not as many cells as the header: not a table.
    expect(draw("| a | b |\n|---|\n| c | d |").querySelector("table")).toBeNull();
  });
});

describe("inline Markdown", () => {
  it("never starts emphasis inside a word", () => {
    const md = draw("snake_case_name, foo_bar_baz et a * b * c, mais _ça_ et *ça* et **gras** et __gras__");
    const p = md.querySelector("p")!;
    expect(p.textContent).toBe("snake_case_name, foo_bar_baz et a * b * c, mais ça et ça et gras et gras");
    expect(texts(p.querySelectorAll("em"))).toEqual(["ça", "ça"]);
    expect(texts(p.querySelectorAll("strong"))).toEqual(["gras", "gras"]);
    // Within a word, a star still emphasises (CommonMark); an underscore never does.
    expect(draw("un*mot*ici").querySelector("em")?.textContent).toBe("mot");
  });

  it("keeps code spans atomic", () => {
    const p = draw("`a_b_c` puis `*x*` puis `[l](https://example.org)` puis `\\*` puis ``un ` dedans``").querySelector("p")!;
    expect(texts(p.querySelectorAll("code"))).toEqual(["a_b_c", "*x*", "[l](https://example.org)", "\\*", "un ` dedans"]);
    expect(p.querySelectorAll("em, strong, a")).toHaveLength(0);
  });

  it("leaves an unclosed backtick as a backtick", () => {
    const md = draw("Puis *ça*, `ici` et un ` seul.\n\nLa suite _reste_ du texte.\n\n- un `point\n- deux");
    const [p1, p2] = [...md.querySelectorAll("p")];
    expect(p1.textContent).toBe("Puis ça, ici et un ` seul.");
    expect(p1.querySelector("code")?.textContent).toBe("ici");
    expect(p1.querySelector("em")?.textContent).toBe("ça");
    expect(p2.textContent).toBe("La suite reste du texte.");
    expect(p2.querySelector("code")).toBeNull();
    expect(texts(md.querySelectorAll("li"))).toEqual(["un `point", "deux"]);
    expect(md.querySelector("pre")).toBeNull();
  });

  it("reads escapes outside code only", () => {
    const p = draw("\\*pas d'emphase\\* et `\\*` et \\_x\\_").querySelector("p")!;
    expect(p.textContent).toBe("*pas d'emphase* et \\* et _x_");
    expect(p.querySelector("em")).toBeNull();
  });

  it("reads links and their words, and keeps unsafe ones as text", () => {
    const md = draw("[le *site*](https://example.org/a_b) et [x](javascript:alert(1)) et [`code_x`](https://example.org)");
    const links = [...md.querySelectorAll("a")];
    expect(links.map((a) => a.getAttribute("href"))).toEqual(["https://example.org/a_b", "https://example.org/"]);
    expect(links[0].querySelector("em")?.textContent).toBe("site");
    expect(links[1].querySelector("code")?.textContent).toBe("code_x");
    expect(md.textContent).toContain("x et");
  });
});

describe("fenced code", () => {
  it("opens only on a fence alone, and closes on a matching one", () => {
    const md = draw("```js\nconst a = 1\n~~~\n```\n\n```inline``` n'ouvre rien\n\nfin");
    expect(texts(md.querySelectorAll("pre"))).toEqual(["const a = 1\n~~~"]);
    expect(texts(md.querySelectorAll("p"))).toEqual(["inline n'ouvre rien", "fin"]);
  });
});

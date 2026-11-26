// @vitest-environment jsdom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { ProposalsContext, type Proposals, type ProposalView } from "../a2ui/proposals";
import { setReads, type NodePage } from "../data/api";
import { makeT, WordsContext } from "../i18n";
import strings from "../strings.json";
import { Doc } from "./Doc";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

// A page's references to catalog servers' items carry what eriphe-core
// places on their lines (page_items.go): the card that waits on an item on
// the row that references it, a warning where the page's words do not say
// the item, a card: link's card where it is written, and the item opened
// below its row.
const body = `## À archiver
- [Facture de Sam][m1] : payée
- [Pub Maison Lune · Gift Guide][m2] : publicité
- [Le rendez-vous][a1] : à garder

[Revenir à la 1.0](card:calls:7)

[Une autre](card:calls:9)

[m1]: monitor:m1
[m2]: monitor:m2
[a1]: agenda:m1
`;

const pageOf = (more: Partial<NodePage> = {}): NodePage => ({
  node: { id: "n1", title: "Mes mails", weight: 0.6, status: "waits", created: "", updated: "", version: 2 },
  version: 2,
  versions: [],
  body,
  items: [
    { line: 2, label: "m1", text: "Facture de Sam", row: 2, id: "m1", open: true, card: "calls:1", title: "Facture de novembre", detail: "Sam <sam@example.org>", external: true },
    { line: 3, label: "m2", text: "Pub Maison Lune · Gift Guide", row: 3, id: "m2", open: true, card: "calls:1", title: "Les clés pour samedi", detail: "Sam <sam@example.org>", external: true, misleading: true },
    { line: 4, label: "a1", text: "Le rendez-vous", row: 4, id: "m1" },
  ],
  placed: ["calls:1", "calls:7"],
  cards: [],
  ...more,
});

const card = (ref: string, text: string): ProposalView => ({ ref, card: Number(ref.split(":")[1]), kind: "calls", state: "pending", verb: "Faire", text });
const views: Record<string, ProposalView> = {
  "calls:1": { ...card("calls:1", "Déplacer 2 mails vers les archives"), verb: "Déplacer les 2" },
  "calls:7": card("calls:7", "Revenir à la version 1.0"),
  "calls:9": card("calls:9", "Une carte d'un autre sujet"),
};

describe("a page's referenced items", () => {
  let el: HTMLDivElement;
  let root: Root;
  let opened: string[];
  beforeEach(() => {
    el = document.createElement("div");
    document.body.append(el);
    root = createRoot(el);
    opened = [];
    setReads({
      page: async () => null,
      trace: async () => [],
      file: async () => null,
      open: async (_id, ref) => {
        opened.push(ref);
        return { title: "Les clés pour samedi", from: "Sam <sam@example.org>", body: "Je passe samedi.", misleading: true };
      },
    });
  });
  afterEach(() => {
    act(() => root.unmount());
    el.remove();
    setReads(null);
  });
  const draw = async (latest = true, page = pageOf()) => {
    const src: Proposals = { read: async (ref) => views[ref] ?? null, answer: async () => true, day: async () => [], follow: () => () => {} };
    await act(async () =>
      root.render(
        <WordsContext.Provider value={{ t: makeT(strings.fr), lang: "fr" }}>
          <ProposalsContext.Provider value={src}>
            <Doc page={page} latest={latest} live />
          </ProposalsContext.Provider>
        </WordsContext.Provider>,
      ),
    );
    await act(async () => {});
  };
  const row = (line: number) => el.querySelector<HTMLElement>(`.md-item[data-line="${line}"]`)!;

  it("puts the card on the rows of the items it acts on, never on another server's", async () => {
    await draw();
    for (const line of [2, 3]) {
      const yes = row(line).querySelector(".md-row [data-row-card='calls:1']");
      expect(yes?.querySelector(".a2-inline-yes")?.textContent).toBe("Déplacer les 2");
      expect(yes?.querySelector(".a2-inline-what")?.textContent).toBe("Déplacer 2 mails vers les archives");
      expect(yes?.querySelector(".a2-inline-shared")).not.toBeNull(); // two rows share it
    }
    expect(row(4).querySelector("[data-row-card]")).toBeNull();
  });

  it("warns where the page's words do not say the item its server read", async () => {
    await draw();
    const lie = row(3).querySelector(".md-ref-lie");
    expect(lie?.querySelector(".md-ref-real")?.textContent).toBe("« Les clés pour samedi » · Sam");
    expect(lie?.textContent).toContain("Les clés pour samedi · Sam <sam@example.org>"); // said to a screen reader
    expect(row(2).querySelector(".md-ref-lie")).toBeNull();
    expect(row(2).querySelector(".md-ref-unread")).toBeNull();
  });

  it("says a referenced item eriphe-core could not read is not checked", async () => {
    const page = pageOf();
    page.items![2] = { ...page.items![2], card: "calls:5", unread: true };
    await draw(true, page);
    expect(row(4).querySelector(".md-ref-unread")?.textContent).toBe("non vérifié");
    expect(row(4).querySelector(".md-ref-lie")).toBeNull();
  });

  it("places a card: link's card where it is written, only one eriphe-core placed", async () => {
    await draw();
    const placed = el.querySelector(".md-card[data-card='calls:7']");
    expect(placed?.querySelector(".a2-proposal-text")?.textContent).toBe("Revenir à la version 1.0");
    expect(el.querySelector("[data-card='calls:9']")).toBeNull();
    expect(el.querySelector(".md-link-card")?.textContent).toBe("Une autre");
  });

  it("opens a referenced item below its row, by its reference, saying the page's words mislead", async () => {
    await draw();
    await act(async () => row(3).querySelector<HTMLElement>(".md-title")!.click());
    await act(async () => {});
    expect(opened).toEqual(["m2"]);
    const read = row(3).querySelector(".md-opened");
    expect(read?.querySelector(".a2-read-title")?.textContent).toBe("Les clés pour samedi");
    expect(read?.querySelector(".a2-read-lie")?.textContent).toContain("« Pub Maison Lune · Gift Guide »");
    // Its card's Oui is there too.
    expect(read?.querySelector(".a2-onyes .a2-inline-yes")?.textContent).toBe("Déplacer les 2");
  });

  it("opens nothing on an older version", async () => {
    await draw(false);
    expect(row(3).querySelector(".md-row")?.getAttribute("role")).toBeNull();
  });

  it("draws every card it places, in a callout too", async () => {
    const md = `> [!figures] Chiffres
> - Compte : 12 € [Virer](card:calls:11)

> [!steps] Le plan
> - [ ] Valider [Valider](card:calls:12)

> [!agenda] Semaine
> ### Jeudi 26 novembre
> - 10:00 Rendez-vous [Accepter](card:calls:13)

> [!person] Sam
> - Rôle : voisin [Répondre](card:calls:14)

> [!warning] Attention
> [Payer](card:calls:15)

> [!note] Côté
> - [Le mail][m1] : à voir

[m1]: monitor:m1
`;
    const placed = ["calls:11", "calls:12", "calls:13", "calls:14", "calls:15", "calls:16"];
    for (const ref of placed) views[ref] ??= card(ref, `Carte ${ref}`);
    await draw(true, pageOf({ body: md, items: [{ line: 18, label: "m1", text: "Le mail", row: 18, id: "m1", card: "calls:16", title: "Le mail" }], placed }));
    for (let i = 0; i < 30; i++) {
      const shut = el.querySelector<HTMLElement>('[aria-expanded="false"]');
      if (!shut) break;
      await act(async () => shut.click());
    }
    for (const ref of placed) expect(el.querySelector(`[data-row-card='${ref}'], [data-card='${ref}']`), ref).not.toBeNull();
  });
});

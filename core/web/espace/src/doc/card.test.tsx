// @vitest-environment jsdom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { ProposalsContext, type Proposals, type ProposalView } from "../a2ui/proposals";
import type { NodePage } from "../data/api";
import { makeT, WordsContext } from "../i18n";
import strings from "../strings.json";
import { pieces } from "./blocks";
import { Doc } from "./Doc";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

// A card block places one of the node's cards in its document: the card
// itself, what it says and where it stands, is eriphe-core's (proposal);
// one that is not the node's shows as not found, with no Oui or Non.
const view = (state: string): ProposalView => ({ ref: "view:3", card: 7, kind: "view", state, text: "Créer la vue « Mes mails »" });

const pageWith = (proposal: ProposalView | undefined): NodePage => ({
  node: { id: "n1", title: "Garder un œil sur mes mails", lifetime: "work", state: "waiting", created: "", updated: "", version: 2 },
  version: 2,
  versions: [],
  parts: [{ text: "## Ce qu'on a décidé\n\nToutes les boîtes.\n" }, { block: { name: "vue", components: [], data: {}, card: "view:3", proposal } }, { text: "La suite.\n" }],
  cards: [],
});

describe("a card block", () => {
  let el: HTMLDivElement;
  let root: Root;
  beforeEach(() => {
    el = document.createElement("div");
    document.body.append(el);
    root = createRoot(el);
  });
  afterEach(() => {
    act(() => root.unmount());
    el.remove();
  });
  const draw = async (page: NodePage, state: string) => {
    const src: Proposals = { read: async (ref) => (ref === "view:3" ? view(state) : null), answer: async () => true, day: async () => [], follow: () => () => {} };
    await act(async () =>
      root.render(
        <WordsContext.Provider value={{ t: makeT(strings.fr), lang: "fr" }}>
          <ProposalsContext.Provider value={src}>
            <Doc page={page} latest live />
          </ProposalsContext.Provider>
        </WordsContext.Provider>,
      ),
    );
    await act(async () => {});
  };

  it("reads as a card piece, between the text", () => {
    expect(pieces(pageWith(view("pending"))).map((p) => p.kind)).toEqual(["text", "card", "text"]);
    expect(pieces(pageWith(view("pending")))[1]).toMatchObject({ kind: "card", ref: "view:3", found: true });
    expect(pieces(pageWith(undefined))[1]).toMatchObject({ kind: "card", found: false });
  });

  it("shows the card where the document places it: Oui and Non while it waits", async () => {
    await draw(pageWith(view("pending")), "pending");
    const card = el.querySelector(".pg-card");
    expect(card?.previousElementSibling?.textContent).toContain("Toutes les boîtes.");
    expect(card?.querySelector(".a2-proposal-text")?.textContent).toBe("Créer la vue « Mes mails »");
    expect(card?.querySelector(".a2-proposal-actions")?.textContent).toBe("OuiNon");
  });

  it("says where it stands once decided, or withdrawn with its node", async () => {
    for (const [state, word] of [
      ["approved", "Validé"],
      ["rejected", "Refusé"],
      ["gone", "Proposition retirée"],
    ]) {
      await draw(pageWith(view(state)), state);
      expect(el.querySelector(".pg-card .a2-proposal-kicker")?.textContent).toContain(word);
      expect(el.querySelector(".pg-card .a2-proposal-actions")).toBeNull();
    }
  });

  it("says a card that is not the node's is not found, with no Oui", async () => {
    await draw(pageWith(undefined), "pending");
    expect(el.querySelector(".pg-card")).toBeNull();
    expect(el.querySelector(".pg-card-missing")?.textContent).toBe("Carte introuvable : elle n'est pas une proposition de ce sujet.");
    expect(el.querySelectorAll("button")).toHaveLength(0);
  });
});

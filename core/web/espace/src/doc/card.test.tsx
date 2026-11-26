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
const skill = (state: string): ProposalView => ({ ref: "skill:3", card: 7, verb: "Faire", kind: "skill", state, text: "Créer le skill « tri-mails »" });

const pageWith = (proposal: ProposalView | undefined): NodePage => ({
  node: { id: "n1", title: "Garder un œil sur mes mails", weight: 0.6, status: "waits", created: "", updated: "", version: 2 },
  version: 2,
  versions: [],
  body: "## Ce qu'on a décidé\n\nToutes les boîtes.\n\n```eriphe:block vue\n{\"card\": \"skill:3\"}\n```\nLa suite.\n",
  parts: [{ text: "## Ce qu'on a décidé\n\nToutes les boîtes.\n" }, { block: { name: "vue", components: [], data: {}, card: "skill:3", proposal } }, { text: "La suite.\n" }],
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
    const src: Proposals = { read: async (ref) => (ref === "skill:3" ? skill(state) : null), answer: async () => true, day: async () => [], follow: () => () => {} };
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
    expect(pieces(pageWith(skill("pending"))).map((p) => p.kind)).toEqual(["text", "card", "text"]);
    expect(pieces(pageWith(skill("pending")))[1]).toMatchObject({ kind: "card", ref: "skill:3", found: true });
    expect(pieces(pageWith(undefined))[1]).toMatchObject({ kind: "card", found: false });
  });

  it("shows the card where the document places it: one Oui and a discreet ✕ while it waits", async () => {
    await draw(pageWith(skill("pending")), "pending");
    const card = el.querySelector(".pg-card");
    expect(card?.previousElementSibling?.textContent).toContain("Toutes les boîtes.");
    expect(card?.querySelector(".a2-proposal-text")?.textContent).toBe("Créer le skill « tri-mails »");
    expect(card?.querySelector(".a2-proposal-actions .a2-button-primary")?.textContent).toBe("Faire");
    expect(card?.querySelector(".a2-proposal-actions .a2-proposal-no")?.getAttribute("aria-label")).toBe("Non");
  });

  it("leaves the body once decided, done, failed, refused or withdrawn with its node; stays while its yes runs", async () => {
    for (const p of [skill("approved"), { ...skill("approved"), exec: "failed" as const }, skill("rejected"), skill("gone")]) {
      expect(pieces(pageWith(p)).map((x) => x.kind)).toEqual(["text", "text"]);
      await draw(pageWith(p), p.state);
      expect(el.querySelector(".pg-card")).toBeNull();
      expect(el.querySelector(".pg-card-missing")).toBeNull();
    }
    const running = { ...skill("approved"), exec: "running" as const };
    expect(pieces(pageWith(running)).map((x) => x.kind)).toEqual(["text", "card", "text"]);
  });

  it("says a card that is not the node's is not found, with no Oui", async () => {
    await draw(pageWith(undefined), "pending");
    expect(el.querySelector(".pg-card")).toBeNull();
    expect(el.querySelector(".pg-card-missing")?.textContent).toBe("Carte introuvable : elle n'est pas une proposition de ce sujet.");
    expect(el.querySelectorAll("button")).toHaveLength(0);
  });
});

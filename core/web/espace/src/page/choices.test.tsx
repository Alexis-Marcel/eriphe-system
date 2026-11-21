// @vitest-environment jsdom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { ProposalsContext, type Answer, type Proposals, type ProposalView } from "../a2ui/proposals";
import { makeT, WordsContext } from "../i18n";
import strings from "../strings.json";
import { Cards } from "./Cards";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

// A card of calls that do the same thing to several items: each a line
// with a box, all ticked; Oui says what it does for those ticked and
// sends them (only) and the others (left); decided, the card says what
// was left. On its own node's page, where it comes from is under Détails.
const pending: ProposalView = {
  ref: "calls:4",
  card: 9,
  kind: "calls",
  state: "pending",
  text: "Archiver 3 mails",
  why: "Skill « tri-mails »",
  tainted: true,
  node: "n1",
  from: "Dans « Mes mails » · 3 éléments du nœud",
  fields: [{ name: "monitor/move", value: "ids : [\"m1\"]", detail: true }],
  choices: [
    { id: 4, text: "« Offre de stage » · Emplois", n: 1, state: "proposed" },
    { id: 5, text: "« La lettre » · news@example.org", n: 1, state: "proposed" },
    { id: 6, text: "« Promo » · shop@example.org", n: 1, state: "proposed" },
  ],
  acts: ["Archiver 1 mail", "Archiver 2 mails", "Archiver 3 mails"],
};

const decided: ProposalView = {
  ...pending,
  state: "approved",
  exec: "done",
  text: "Archiver 2 mails · 1 laissé",
  acts: undefined,
  choices: pending.choices!.map((c) => ({ ...c, state: c.id === 5 ? "rejected" : "approved" })),
};

describe("a card of several calls", () => {
  let el: HTMLDivElement;
  let root: Root;
  let answers: Answer[];
  let shown: ProposalView;
  beforeEach(() => {
    el = document.createElement("div");
    document.body.append(el);
    root = createRoot(el);
    answers = [];
    shown = pending;
  });
  afterEach(() => {
    act(() => root.unmount());
    el.remove();
  });
  const draw = async (home?: string) => {
    const src: Proposals = {
      read: async () => shown,
      answer: async (a) => {
        answers.push(a);
        shown = decided;
        return true;
      },
      day: async () => [],
      follow: () => () => {},
    };
    await act(async () =>
      root.render(
        <WordsContext.Provider value={{ t: makeT(strings.fr), lang: "fr" }}>
          <ProposalsContext.Provider value={src}>
            <Cards surface="n1--cards" refs={["calls:4"]} home={home} />
          </ProposalsContext.Provider>
        </WordsContext.Provider>,
      ),
    );
    await act(async () => {});
  };
  const yes = () => el.querySelector<HTMLButtonElement>(".a2-proposal-actions .a2-button-primary")!;
  const boxes = () => [...el.querySelectorAll<HTMLInputElement>(".a2-proposal-choices input[type=checkbox]")];

  it("lists each call ticked, and Oui says what goes", async () => {
    await draw("n1");
    expect(boxes().map((b) => b.checked)).toEqual([true, true, true]);
    expect([...el.querySelectorAll(".a2-proposal-choices li")].map((li) => li.textContent)).toEqual([
      "« Offre de stage » · Emplois",
      "« La lettre » · news@example.org",
      "« Promo » · shop@example.org",
    ]);
    expect(yes().textContent).toBe("Archiver 3 mails");
    await act(async () => boxes()[1].click());
    expect(yes().textContent).toBe("Archiver 2 mails");
    await act(async () => boxes()[0].click());
    await act(async () => boxes()[2].click());
    expect(yes().disabled).toBe(true);
    await act(async () => boxes()[0].click());
    expect(yes().textContent).toBe("Archiver 1 mail");
  });

  it("sends the ticked and the unticked, and says what was left", async () => {
    await draw("n1");
    await act(async () => boxes()[1].click());
    await act(async () => yes().click());
    expect(answers).toHaveLength(1);
    expect(answers[0]).toMatchObject({ ref: "calls:4", yes: true, only: [4, 6], left: [5] });
    expect(el.querySelector(".a2-proposal-text")?.textContent).toBe("Archiver 2 mails · 1 laissé");
    expect(boxes()).toHaveLength(0);
    const left = el.querySelector(".a2-proposal-choices li.is-left");
    expect(left?.textContent).toBe("« La lettre » · news@example.orglaissé");
  });

  it("one call: no box, but Oui names it", async () => {
    shown = { ...pending, text: "Archiver 1 mail", choices: [pending.choices![0]], acts: undefined };
    await draw("n1");
    expect(boxes()).toHaveLength(0);
    expect(yes().textContent).toBe("Oui");
    await act(async () => yes().click());
    expect(answers[0]).toMatchObject({ yes: true, only: [4], left: [] });
  });

  it("Non sends no choice", async () => {
    await draw("n1");
    await act(async () => boxes()[1].click());
    await act(async () => el.querySelectorAll<HTMLButtonElement>(".a2-proposal-actions button")[1].click());
    expect(answers[0].yes).toBe(false);
    expect(answers[0].only).toBeUndefined();
  });

  it("on its own node's page, keeps where it comes from under Détails; the external sign stays", async () => {
    await draw("n1");
    const card = el.querySelector(".a2-proposal")!;
    const details = card.querySelector(".a2-proposal-details")!;
    for (const cls of ["a2-proposal-why", "a2-proposal-from"]) {
      expect(card.querySelector(`:scope > .${cls}`)).toBeNull();
      expect(details.querySelector(`.${cls}`)).not.toBeNull();
    }
    expect(card.querySelector(".a2-proposal-yes")).toBeNull(); // its sentence says what Oui does
    expect(card.querySelector(".a2-taint")).not.toBeNull();
  });

  it("elsewhere, says where it comes from", async () => {
    await draw();
    const card = el.querySelector(".a2-proposal")!;
    expect(card.querySelector(":scope > .a2-proposal-why")?.textContent).toBe("Skill « tri-mails »");
    expect(card.querySelector(":scope > .a2-proposal-from")?.textContent).toContain("Mes mails");
  });
});

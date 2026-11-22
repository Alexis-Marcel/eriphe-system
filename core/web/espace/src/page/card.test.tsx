// @vitest-environment jsdom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { ProposalsContext, type Answer, type Moved, type Proposals } from "../a2ui/proposals";
import { makeT, WordsContext, type Lang } from "../i18n";
import strings from "../strings.json";
import { Card } from "./Card";

// A card as the page draws it (no catalog component says yes): what it
// asks by its kind, what its yes makes first, its technical words one
// click away, its one Oui and its discreet ✕, where it stands once
// answered here or anywhere else.

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

let el: HTMLDivElement;
let root: Root;

const quiet = { day: async () => [], follow: () => () => {} };

async function draw(lang: Lang, ref: string, src: Partial<Proposals>) {
  root = createRoot(el);
  await act(async () =>
    root.render(
      <WordsContext.Provider value={{ t: makeT(strings[lang]), lang }}>
        <ProposalsContext.Provider value={{ ...quiet, answer: async () => true, read: async () => null, ...src } as Proposals}>
          <Card refText={ref} surface="console" />
        </ProposalsContext.Provider>
      </WordsContext.Provider>,
    ),
  );
  await act(async () => {});
}

beforeEach(() => {
  el = document.createElement("div");
  document.body.append(el);
});
afterEach(() => {
  act(() => root.unmount());
  el.remove();
});

const $ = (sel: string) => el.querySelector(sel) as HTMLElement;
const $$ = (sel: string) => [...el.querySelectorAll(sel)] as HTMLElement[];
const text = (sel: string) => $(sel)?.textContent ?? null;
const click = (n: Element) => act(async () => (n as HTMLElement).click());
const yes = () => $(".a2-proposal-actions .a2-button-primary") as HTMLButtonElement;
const no = () => $(".a2-proposal-actions .a2-proposal-no") as HTMLButtonElement;

describe("a card", () => {
  it("answers the one proposal it shows, and says how it was decided", async () => {
    const sent: Answer[] = [];
    let state = "pending";
    await draw("fr", "rule:7", {
      read: async () => ({ ref: "rule:7", card: 7, state, text: "Ne plus te prévenir des factures avant le brief ?", why: "Tu n'as ouvert aucune des cinq dernières." }),
      answer: async (a) => {
        sent.push(a);
        state = "approved";
        return true;
      },
    });
    expect(text(".a2-proposal-text")).toBe("Ne plus te prévenir des factures avant le brief ?");
    expect(text(".a2-proposal-why")).toBe("Tu n'as ouvert aucune des cinq dernières.");
    expect(yes().textContent).toBe("Oui");
    expect(no().getAttribute("aria-label")).toBe("Non");
    await click(yes());
    expect(sent).toMatchObject([{ ref: "rule:7", yes: true }]);
    expect($(".a2-proposal-actions")).toBeNull();
    expect(text(".a2-proposal-kicker")).toBe("Validé");
  });

  it("leads a view's card with what its yes makes, its technical words one click away", async () => {
    const view = {
      ref: "view:1",
      card: 4,
      kind: "view",
      state: "pending",
      text: "Créer la vue « Mes mails » ?",
      why: "Les mails qui demandent une réponse",
      summary: ["Une page « Mes mails » dans ton graphe, tenue à jour", "Regarde toutes les 5 min s'il y a du nouveau"],
      fields: [
        { name: "Moniteur mails-recus", value: "toutes les 5 min, regarde mail.recus", detail: true },
        { name: "mails-trier · Consignes", value: "# Trier les mails\n\nPour **chaque** mail :\n\n1. Note-le.\n2. Prépare une réponse.", detail: true, markdown: true },
        { name: "Dépôts", value: "sam/site" },
      ],
    };
    await draw("fr", "view:1", { read: async () => view });
    expect($$(".a2-proposal-summary li").map((li) => li.textContent)).toEqual(view.summary);
    const details = $("details.a2-proposal-details") as HTMLDetailsElement;
    expect(details.open).toBe(false);
    expect($$(".a2-proposal > .a2-proposal-fields dt").map((d) => d.textContent)).toEqual(["Dépôts"]);
    expect([...details.querySelectorAll("dt")].map((d) => d.textContent)).toEqual(["Moniteur mails-recus", "mails-trier · Consignes"]);
    const md = details.querySelector(".a2-proposal-md dd")!;
    expect(md.querySelector("h3")?.textContent).toBe("Trier les mails");
    expect(md.textContent).not.toContain("**");
    expect(details.querySelector(".a2-proposal-actions")).toBeNull();
  });

  it("updates a proposal answered elsewhere, and only on its own move", async () => {
    let state = "pending";
    let reads = 0;
    const followers = new Set<(m: Moved) => void>();
    const move = (m: Moved) => act(async () => followers.forEach((f) => f(m)));
    await draw("fr", "rule:9", {
      read: async () => (reads++, { ref: "rule:9", card: 9, state, text: "Ranger les mails ?" }),
      follow: (on) => (followers.add(on), () => followers.delete(on)),
    });
    const before = reads;
    await move({ refs: ["rule:10"] });
    expect(reads).toBe(before);
    state = "approved";
    await move({ refs: ["rule:9"] });
    await act(async () => {});
    expect($(".a2-proposal-actions")).toBeNull();
    expect(text(".a2-proposal-kicker")).toBe("Validé");
    state = "rejected";
    await move({ refs: [], all: true });
    await act(async () => {});
    expect(text(".a2-proposal-kicker")).toBe("Refusé");
  });

  it("follows an approved call: running, then done or failed with our reason", async () => {
    let view: Record<string, unknown> = { ref: "access:4", card: 4, state: "approved", exec: "running", text: "Fusionner ?" };
    const followers = new Set<(m: Moved) => void>();
    const move = (m: Moved) => act(async () => followers.forEach((f) => f(m)));
    await draw("en", "access:4", { read: async () => view as never, follow: (on) => (followers.add(on), () => followers.delete(on)) });
    expect(text(".a2-proposal-kicker")).toBe("Approved · running…");
    view = { ...view, exec: "failed", execWhy: "error" };
    await move({ refs: ["access:4"] });
    await act(async () => {});
    expect(text(".a2-proposal-kicker")).toBe("Approved · failed");
    expect(text(".a2-proposal-failed")).toBe("The tool answered with an error.");
  });

  it("says when a proposal is gone, or an answer did not leave", async () => {
    await draw("fr", "rule:404", {});
    expect(text(".a2-empty")).toBe("Cette proposition n'existe plus.");
    act(() => root.unmount());
    await draw("fr", "rule:9", { read: async () => ({ ref: "rule:9", card: 9, state: "pending", text: "Ranger les mails ?" }), answer: async () => false });
    await click(no());
    expect(text(".a2-proposal-failed")).toBe("La réponse n'est pas partie : réessaie.");
    expect($(".a2-proposal-actions")).not.toBeNull();
  });

  it("says what it asks by its kind: keep a memory or a reminder, or do something", async () => {
    for (const lang of ["fr", "en"] as const) {
      const fr = lang === "fr";
      for (const [kind, kicker, yesWord, noWord] of [
        ["memory", fr ? "Garder en mémoire ?" : "Keep in memory?", fr ? "Garder" : "Keep", fr ? "Oublier" : "Forget"],
        ["intention", fr ? "Garder ce rappel ?" : "Keep this reminder?", fr ? "Garder" : "Keep", fr ? "Oublier" : "Forget"],
        ["access", fr ? "Proposition" : "Proposal", fr ? "Oui" : "Yes", fr ? "Non" : "No"],
      ]) {
        await draw(lang, `${kind}:1`, { read: async (r) => ({ ref: r, card: 1, kind, state: "pending", text: "x" }) });
        expect(text(".a2-proposal-kicker")).toBe(kicker);
        expect(yes().textContent).toBe(yesWord);
        expect(no().getAttribute("aria-label")).toBe(noWord);
        act(() => root.unmount());
      }
    }
    await draw("fr", "memory:1", { read: async (r) => ({ ref: r, card: 1, kind: "memory", state: "approved", text: "x" }) });
    expect(text(".a2-proposal-kicker")).toBe("Gardé");
  });

  it("marks a proposal drawn from external content with a small sign, no word", async () => {
    await draw("fr", "rule:11", { read: async () => ({ ref: "rule:11", card: 11, state: "pending", text: "Classer la facture ?", tainted: true }) });
    const sign = $(".a2-proposal-kicker .a2-taint");
    expect(sign.getAttribute("role")).toBe("img");
    expect(sign.getAttribute("aria-label")).toBe("Vient d'un contenu extérieur : vérifie ce qui va être fait.");
    expect($(".a2-proposal-kicker").textContent).not.toContain("extérieur");
  });

  it("says where a proposal comes from, linked to its node, and what its Oui does while it waits", async () => {
    const view = {
      ref: "access:4", card: 4, kind: "access", state: "pending", text: "Autoriser ?",
      node: "k3m9", from: "Bulle « PR alerting »", yes: "Sur Oui, la tâche peut modifier dans github jusqu'à sa fin.",
    };
    await draw("fr", "access:4", { read: async () => view });
    expect($(".a2-proposal-from a").getAttribute("href")).toBe("/espace?open=node-k3m9");
    expect(text(".a2-proposal-yes")).toBe("Sur Oui, la tâche peut modifier dans github jusqu'à sa fin.");
  });

  it("says at once that an answer is on its way", async () => {
    let done!: (ok: boolean) => void;
    await draw("en", "rule:12", { read: async () => ({ ref: "rule:12", card: 12, state: "pending", text: "Ranger ?" }), answer: () => new Promise<boolean>((r) => (done = r)) });
    const [y, n] = [yes(), no()];
    await act(async () => y.click());
    expect(y.classList.contains("is-busy")).toBe(true);
    expect(n.disabled).toBe(true);
    await act(async () => done(true));
  });

  it("lets the user untick the calls of a card of several, Oui saying how many go", async () => {
    const sent: Answer[] = [];
    await draw("fr", "calls:3", {
      read: async () => ({
        ref: "calls:3",
        card: 3,
        kind: "calls",
        state: "pending",
        text: "Archiver 2 mails",
        choices: [
          { id: 1, text: "Pub", n: 1, state: "proposed" },
          { id: 2, text: "Lettre", n: 1, state: "proposed" },
        ],
        acts: ["Archiver 1 mail", "Archiver 2 mails"],
      }),
      answer: async (a) => (sent.push(a), true),
    });
    expect(yes().textContent).toBe("Archiver 2 mails");
    await click($$(".a2-proposal-choices input")[1]);
    expect(yes().textContent).toBe("Archiver 1 mail");
    await click(yes());
    expect(sent).toMatchObject([{ ref: "calls:3", yes: true, only: [1], left: [2] }]);
  });
});

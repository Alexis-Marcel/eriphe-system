// @vitest-environment jsdom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { CATALOG } from "./a2ui/catalog";
import * as data from "./data";
import { Espace } from "./Espace";
import { cardTalk, clock, landed, plain, present, TALK_OVER } from "./Exchange";
import { WordsContext, makeT } from "./i18n";
import { localURL } from "./presence";
import type { Layout } from "./types";

// The workspace is a full home: it tells eriphe-core the user is there
// (presence), shows the notifications routed to it, and the visit's
// conversation, Eriphe's reply as it comes; what is said about a thread
// shows in its card, then folds into one line of it (#556).

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

class FakeSource {
  static all: FakeSource[] = [];
  url: string;
  closed = false;
  listeners = new Map<string, Set<EventListener>>();
  constructor(url: string) {
    this.url = url;
    FakeSource.all.push(this);
  }
  addEventListener(type: string, h: EventListener) {
    if (!this.listeners.has(type)) this.listeners.set(type, new Set());
    this.listeners.get(type)!.add(h);
  }
  removeEventListener(type: string, h: EventListener) {
    this.listeners.get(type)?.delete(h);
  }
  close() {
    this.closed = true;
  }
  static emit(type: string, payload: unknown) {
    const es = FakeSource.all.filter((s) => !s.closed).pop()!;
    const e = new MessageEvent(type, { data: JSON.stringify(payload) });
    for (const h of es.listeners.get(type) ?? []) h(e);
  }
}

type Post = { url: string; form: URLSearchParams | null; headers: Record<string, string> };

let el: HTMLDivElement;
let root: Root;
let posts: Post[];
let presenceNotes: data.Note[];
let visible: DocumentVisibilityState;

const words = {
  "espace.exchange": "La conversation de cette visite",
  "espace.exchange.writing": "Eriphe écrit…",
  "espace.exchange.all": "Toute la conversation",
  "espace.talk": "Échange sur ce fil",
  "espace.talk.line": "%s : tu as écrit « %s » ; Eriphe a répondu « %s »",
  "espace.talk.line.alone": "%s : tu as écrit « %s »",
  "espace.talk.trace": "Tu as parlé de %s : l'échange est dans sa carte",
  "espace.talk.about": "À propos de %s ↗",
  "espace.talk.external": "Écrit après avoir lu un contenu extérieur",
  "espace.notice.close": "Fermer",
};

beforeEach(() => {
  el = document.createElement("div");
  document.body.append(el);
  root = createRoot(el);
  FakeSource.all = [];
  posts = [];
  presenceNotes = [];
  visible = "visible";
  vi.stubGlobal("EventSource", FakeSource);
  vi.stubGlobal("matchMedia", (q: string) => ({ matches: false, media: q, addEventListener() {}, removeEventListener() {} }));
  Object.defineProperty(document, "visibilityState", { configurable: true, get: () => visible });
  vi.spyOn(document, "hasFocus").mockImplementation(() => visible === "visible");
  vi.stubGlobal("fetch", async (url: string, init?: RequestInit) => {
    const form = init?.body instanceof URLSearchParams ? init.body : null;
    posts.push({ url, form, headers: (init?.headers ?? {}) as Record<string, string> });
    if (url === "/presence" && form?.get("front") === "1" && presenceNotes.length > 0) {
      const notes = presenceNotes;
      presenceNotes = [];
      return new Response(JSON.stringify({ notes }), { status: 200, headers: { "Content-Type": "application/json" } });
    }
    return new Response(null, { status: 204 });
  });
});

afterEach(async () => {
  await act(async () => root.unmount());
  el.remove();
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

const draw = () =>
  act(async () =>
    root.render(
      <WordsContext.Provider value={{ t: makeT(words), lang: "fr" }}>
        <Espace boot={{ lang: "fr", name: "Eriphe", csrf: "tok", strings: words, live: true }} />
      </WordsContext.Provider>,
    ),
  );

const settle = () => act(async () => new Promise((r) => setTimeout(r, 0)));
const presence = () => posts.filter((p) => p.url === "/presence");

describe("the workspace's presence", () => {
  it("tells eriphe-core when it is in front and when it leaves, with its tab and the CSRF token", async () => {
    await draw();
    await settle();
    const first = presence()[0];
    expect(first.form?.get("front")).toBe("1");
    expect(first.form?.get("tab")).toBe(data.tab);
    expect(first.form?.get("csrf")).toBe("tok");
    expect(first.headers["X-CSRF-Token"]).toBe("tok");
    // The stream carries the same tab: the notifications routed to it come by it too.
    expect(FakeSource.all.some((s) => s.url === `/events?a2ui=1&tab=${data.tab}`)).toBe(true);

    visible = "hidden";
    await act(async () => void document.dispatchEvent(new Event("visibilitychange")));
    expect(presence().at(-1)!.form?.get("front")).toBe("0");
    const n = presence().length;
    await act(async () => void document.dispatchEvent(new Event("visibilitychange")));
    expect(presence()).toHaveLength(n); // no change, nothing said
    visible = "visible";
    await act(async () => void document.dispatchEvent(new Event("visibilitychange")));
    expect(presence().at(-1)!.form?.get("front")).toBe("1");
  });

  it("shows the notes routed to it, by its presence or its stream; a note goes where it says", async () => {
    presenceNotes = [{ id: 1, body: { fr: "Le colis est arrivé", en: "The parcel came" }, url: "/conversation#m3" }];
    await draw();
    await settle();
    const a = el.querySelector<HTMLAnchorElement>(".notices .notice a")!;
    expect(a.textContent).toBe("Le colis est arrivé");
    expect(a.getAttribute("href")).toBe("/conversation#m3");

    await act(async () => FakeSource.emit("notice", [{ id: 2, body: { fr: "Ailleurs" }, url: "//elsewhere.example.org/" }]));
    const notes = el.querySelectorAll(".notices .notice");
    expect(notes).toHaveLength(2);
    expect(notes[1].querySelector("a")).toBeNull(); // not a path of this site: no link
    expect(notes[1].textContent).toContain("Ailleurs");

    await act(async () => el.querySelector<HTMLButtonElement>(".notice-close")!.click());
    expect(el.querySelectorAll(".notices .notice")).toHaveLength(1);
  });

  it("registers the service worker and tells its push subscription", async () => {
    const onMessage: ((e: MessageEvent) => void)[] = [];
    const register = vi.fn(async () => ({}));
    const worker = {
      register,
      addEventListener: (_: string, h: (e: MessageEvent) => void) => onMessage.push(h),
      removeEventListener: () => {},
      getRegistration: async () => ({ pushManager: { getSubscription: async () => ({ endpoint: "https://push.example.org/sub/1" }) } }),
    };
    Object.defineProperty(navigator, "serviceWorker", { configurable: true, value: worker });
    Object.defineProperty(window, "isSecureContext", { configurable: true, value: true });
    try {
      await draw();
      for (let i = 0; i < 5; i++) await settle();
      expect(register).toHaveBeenCalledWith("/sw.js");
      const sub = presence().at(-1)!.form?.get("sub") ?? "";
      expect(sub).toMatch(/^[0-9a-f]{32}$/);
      // A notification clicked for another origin: the page stays.
      const before = location.href;
      for (const h of onMessage) h(new MessageEvent("message", { data: { type: "open", url: "https://elsewhere.example.org/x" } }));
      expect(location.href).toBe(before);
    } finally {
      delete (navigator as { serviceWorker?: unknown }).serviceWorker;
      delete (window as { isSecureContext?: unknown }).isSecureContext;
    }
  });

  it("takes only this site's paths as a note's link", () => {
    expect(localURL("/conversation#m1")).toBe(true);
    expect(localURL("//example.org/x")).toBe(false);
    expect(localURL("https://example.org/x")).toBe(false);
    expect(localURL(undefined)).toBe(false);
    expect(localURL("/\\evil.example")).toBe(false);
    expect(localURL("/\\/evil.example")).toBe(false);
    expect(localURL("/\t/evil.example")).toBe(false);
    expect(localURL("/\n/evil.example")).toBe(false);
    expect(localURL("/settings/tools?q=a")).toBe(true);
  });
});

const surface = (id: string, words: string) => ({
  version: "1.0",
  createSurface: { surfaceId: id, catalogId: CATALOG.id, components: [{ id: "root", component: "Text", text: words }] },
});

// A layout with the thread's surface open, or none open.
const layout = (focus?: string): Layout => ({
  eriphe: "Bonjour. Trois fils avancent.",
  by: "composer",
  ambient: "calm",
  focus,
  items: [
    { id: "thread-3", title: "Site portfolio", size: "l", col: 0, row: 0, w: 6, h: 3 },
    { id: "today", title: "Aujourd'hui", size: "m", col: 6, row: 0, w: 3, h: 2 },
  ],
  chips: [],
});

const at = (min: number) => new Date(Date.now() - min * 60_000).toISOString();
const card = () => el.querySelector<HTMLElement>('article[data-surface="thread-3"]')!;
const left = () => el.querySelector<HTMLElement>(".present")!;

async function open(focus?: string) {
  await draw();
  await act(async () => {
    FakeSource.emit("a2ui", { seq: 1, reset: true, messages: [surface("thread-3", "Le site du portfolio."), surface("today", "Trois réunions")] });
    FakeSource.emit("layout", layout(focus));
  });
}

async function submit(text: string) {
  const input = el.querySelector<HTMLInputElement>(".eriphe-input input")!;
  input.value = text;
  await act(async () => void el.querySelector("form")!.dispatchEvent(new Event("submit", { bubbles: true, cancelable: true })));
  await settle();
}

describe("the visit's conversation, in the left column", () => {
  it("starts with the arrival message, large while nothing follows; then the visit, Eriphe's newest line large", async () => {
    await open();
    await act(async () => new Promise((r) => setTimeout(r, 400))); // typed
    const first = left().querySelector(".present-arrival")!;
    expect(first.textContent).toContain("Bonjour. Trois fils avancent.");
    expect(first.classList.contains("latest")).toBe(true);
    expect(el.querySelector<HTMLAnchorElement>(".eriphe-all")!.getAttribute("href")).toBe("/conversation");

    await submit("quel temps demain ?");
    const sent = posts.find((p) => p.url === "/messages")!;
    expect(sent.form?.get("text")).toBe("quel temps demain ?");
    expect(sent.form?.get("about")).toBeNull(); // nothing open
    // Shown at once, as a bubble, Eriphe at work; the arrival no longer large.
    expect(left().querySelector(".present-bubble")!.textContent).toBe("quel temps demain ?");
    expect(left().querySelector(".exchange-dots")).not.toBeNull();
    expect(first.classList.contains("latest")).toBe(false);

    await act(async () => FakeSource.emit("exchange", { lines: [{ seq: 5, role: "user", text: "quel temps demain ?", time: at(1) }], draft: "Demain, il" }));
    expect(left().querySelectorAll(".present-bubble")).toHaveLength(1);
    expect(left().querySelector(".present-brain.draft")!.textContent).toBe("Demain, il");
    await act(async () =>
      FakeSource.emit("exchange", {
        lines: [
          { seq: 5, role: "user", text: "quel temps demain ?", time: at(1) },
          { seq: 6, role: "brain", text: "Demain, il **pleut**. <img src=x onerror=alert(1)>", time: at(0), external: true },
        ],
      }),
    );
    const reply = left().querySelector(".present-brain:not(.draft)")!;
    expect(reply.querySelector("strong")!.textContent).toBe("pleut");
    expect(reply.querySelector("img")).toBeNull(); // Markdown, never HTML
    expect(reply.classList.contains("latest")).toBe(true);
    expect(reply.querySelector(".talk-external")).not.toBeNull(); // written after reading external content
    expect(left().querySelector(".exchange-dots")).toBeNull();

    // A newer line: the reply is no longer the large one; an error says only that Eriphe could not answer.
    await act(async () =>
      FakeSource.emit("exchange", {
        lines: [
          { seq: 5, role: "user", text: "quel temps demain ?", time: at(1) },
          { seq: 6, role: "brain", text: "Demain, il pleut.", time: at(1) },
          { seq: 7, role: "user", text: "et après ?", time: at(0) },
          { seq: 8, role: "error", time: at(0) },
        ],
      }),
    );
    expect(left().querySelector(".present-brain.latest")).toBeNull();
    expect(left().querySelector(".exchange-error")).not.toBeNull();
  });
});

describe("an exchange about a thread", () => {
  const talk = (min = 1) => ({
    lines: [
      { seq: 5, role: "user", text: "mets plutôt un formulaire Formspree", time: at(min + 1), thread: "thread-3" },
      { seq: 6, role: "brain", text: "D'accord : **Formspree** plutôt que mailto.", time: at(min), thread: "thread-3" },
    ],
  });

  it("shows in the thread's card while it goes on; the left column keeps one line of it", async () => {
    await open("thread-3");
    await submit("mets plutôt un formulaire Formspree");
    expect(posts.find((p) => p.url === "/messages")!.form?.get("about")).toBe("surface:thread-3");
    // At once in the card, Eriphe at work there; one line on the left.
    expect(card().querySelector(".talk-open .talk-bubble")!.textContent).toBe("mets plutôt un formulaire Formspree");
    expect(card().querySelector(".talk .exchange-dots")).not.toBeNull();
    expect(left().querySelector(".present-bubble")).toBeNull();
    expect(left().querySelector(".present-trace")!.textContent).toContain("Site portfolio");
    expect(left().querySelector(".exchange-dots")).toBeNull();

    const x = talk();
    await act(async () => FakeSource.emit("exchange", { lines: x.lines.slice(0, 1), threads: { "thread-3": [{ lines: x.lines.slice(0, 1) }] }, draft: "D'accord", draftThread: "thread-3" }));
    expect(card().querySelector(".talk-brain.draft")!.textContent).toBe("D'accord");
    expect(left().querySelector(".present-brain")).toBeNull();
    await act(async () => FakeSource.emit("exchange", { lines: x.lines, threads: { "thread-3": [x] } }));
    expect(card().querySelector(".talk-open .talk-brain strong")!.textContent).toBe("Formspree");
    expect(left().querySelectorAll(".present-trace")).toHaveLength(1);
    expect(left().querySelector(".present-brain")).toBeNull();
    // Something was said since the arrival message: it is no longer the large one.
    expect(left().querySelector(".present-arrival.latest")).toBeNull();
  });

  it("folds into one line of the card once over, which unfolds on a click", async () => {
    await open("thread-3");
    const x = talk(30); // half an hour without a word
    await act(async () => FakeSource.emit("exchange", { lines: x.lines, threads: { "thread-3": [x] } }));
    expect(card().querySelector(".talk-open")).toBeNull();
    const line = card().querySelector<HTMLButtonElement>(".talk-fold-line")!;
    expect(line.textContent).toMatch(/^\d+ h \d\d : tu as écrit « mets plutôt un formulaire Formspree » ; Eriphe a répondu « D'accord : Formspree plutôt que mailto. »$/);
    expect(line.getAttribute("aria-expanded")).toBe("false");
    await act(async () => line.click());
    expect(card().querySelector(".talk-fold-body .talk-brain strong")!.textContent).toBe("Formspree");
    expect(line.getAttribute("aria-expanded")).toBe("true");
  });

  it("shows whole on the left, with the way to its card, when the card is not open", async () => {
    await open();
    const x = talk();
    await act(async () => FakeSource.emit("exchange", { lines: x.lines, threads: { "thread-3": [x] } }));
    expect(left().querySelector(".present-bubble")!.textContent).toBe("mets plutôt un formulaire Formspree");
    expect(left().querySelector(".present-brain strong")!.textContent).toBe("Formspree");
    expect(card().querySelector(".talk")).toBeNull(); // a tile shows no exchange
    await act(async () => left().querySelector<HTMLButtonElement>(".present-about")!.click());
    expect(posts.some((p) => p.url === "/api/espace/action")).toBe(true);
  });
});

describe("what the page places", () => {
  const user = (seq: number, thread?: string, min = 1) => ({ seq, role: "user" as const, text: "m" + seq, time: at(min), thread });
  const brain = (seq: number, thread?: string, min = 1) => ({ seq, role: "brain" as const, text: "r" + seq, time: at(min), thread });

  it("knows when the message sent has landed", () => {
    const sent = { text: "salut", after: 4 };
    expect(landed({ lines: [brain(4)] }, sent)).toBe(false);
    expect(landed({ lines: [user(5)] }, sent)).toBe(true);
  });

  it("files the exchange about the open thread in its card, the rest on the left", () => {
    const x = { lines: [user(1), brain(2), user(3, "thread-3"), brain(4, "thread-3")], threads: { "thread-3": [{ lines: [user(3, "thread-3"), brain(4, "thread-3")] }] } };
    expect(present(x, null, "thread-3").items.map((i) => i.kind)).toEqual(["line", "line", "trace"]);
    expect(present(x, null, null).items.map((i) => i.kind)).toEqual(["line", "line", "line", "line"]);
    expect(cardTalk(x, null, "thread-3", Date.now()).open.map((l) => l.seq)).toEqual([3, 4]);
    expect(cardTalk(x, null, "today", Date.now()).open).toEqual([]);
  });

  it("calls an exchange over when a newer message came, or after a while without a word", () => {
    const t3 = { lines: [user(3, "thread-3", 2), brain(4, "thread-3", 2)] };
    const newer = { lines: [user(3, "thread-3", 2), brain(4, "thread-3", 2), user(5)], threads: { "thread-3": [t3] } };
    expect(cardTalk(newer, null, "thread-3", Date.now()).folded).toHaveLength(1);
    const quiet = { lines: t3.lines, threads: { "thread-3": [t3] } };
    expect(cardTalk(quiet, null, "thread-3", Date.now()).open).toHaveLength(2);
    expect(cardTalk(quiet, null, "thread-3", Date.now() + TALK_OVER).folded).toHaveLength(1);
    // Eriphe still at work on it: not over.
    expect(cardTalk({ ...quiet, thinking: true }, null, "thread-3", Date.now() + TALK_OVER).open).toHaveLength(2);
    // A message sent about it: the one before folds, the new one opens.
    const v = cardTalk(quiet, { text: "et après ?", after: 4, about: "thread-3" }, "thread-3", Date.now());
    expect(v.open[0].text).toBe("et après ?");
    expect(v.folded).toHaveLength(1);
  });

  it("words a moment and a glimpse as the thread's header does", () => {
    const now = new Date(2026, 10, 15, 16, 30);
    expect(clock(new Date(2026, 10, 15, 16, 10).toISOString(), "fr", now)).toBe("16 h 10");
    expect(clock(new Date(2026, 10, 15, 9, 5).toISOString(), "en", now)).toBe("09:05");
    expect(clock(new Date(2026, 10, 13, 16, 10).toISOString(), "fr", now)).toMatch(/^13 nov\.? 16 h 10$/);
    expect(plain("D'accord : **Formspree**, voir [la page](https://example.org).", 100)).toBe("D'accord : Formspree, voir la page.");
    expect(plain("a".repeat(20), 10)).toBe("a".repeat(9) + "…");
  });
});

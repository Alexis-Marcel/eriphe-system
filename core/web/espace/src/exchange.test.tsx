// @vitest-environment jsdom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { CATALOG } from "./a2ui/catalog";
import * as data from "./data";
import { Espace } from "./Espace";
import { landed, present } from "./Exchange";
import { WordsContext, makeT } from "./i18n";
import { localURL } from "./presence";
import type { Layout } from "./types";

// The workspace is a full home: it tells eriphe-core the user is there
// (presence), shows the notifications routed to it, and the conversation's
// last exchange, Eriphe's reply as it comes; scrolling up the column
// brings the messages before it.

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
let earlierPages: Record<number, data.Earlier>; // GET /api/espace/earlier, by before

const words = {
  "espace.exchange": "Le dernier échange",
  "espace.exchange.writing": "Eriphe écrit…",
  "espace.exchange.all": "Toute la conversation",
  "espace.exchange.earlier": "Messages d'avant",
  "espace.exchange.external": "Écrit après avoir lu un contenu extérieur",
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
  earlierPages = {};
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
    const before = /^\/api\/espace\/earlier\?before=(\d+)$/.exec(url);
    if (before) {
      const page = earlierPages[Number(before[1])] ?? { lines: [], more: false };
      return new Response(JSON.stringify(page), { status: 200, headers: { "Content-Type": "application/json" } });
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
const bubbles = () => [...left().querySelectorAll(".present-bubble")].map((b) => b.textContent);

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

describe("the last exchange, in the left column", () => {
  it("starts with the arrival message, large while nothing follows; then the last exchange, Eriphe's newest line large", async () => {
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
    expect(reply.querySelector(".exchange-external")).not.toBeNull(); // written after reading external content
    expect(left().querySelector(".exchange-dots")).toBeNull();

    // An error says only that Eriphe could not answer.
    await act(async () =>
      FakeSource.emit("exchange", {
        lines: [
          { seq: 7, role: "user", text: "et après ?", time: at(0) },
          { seq: 8, role: "error", time: at(0) },
        ],
      }),
    );
    expect(left().querySelector(".present-brain.latest")).toBeNull();
    expect(left().querySelector(".exchange-error")).not.toBeNull();
  });

  it("shows only the last exchange: a new message replaces the one before at once", async () => {
    await open();
    await act(async () =>
      FakeSource.emit("exchange", {
        lines: [
          { seq: 5, role: "user", text: "quel temps demain ?", time: at(2) },
          { seq: 6, role: "brain", text: "Demain, il pleut.", time: at(2) },
        ],
        earlier: true,
      }),
    );
    expect(bubbles()).toEqual(["quel temps demain ?"]);
    await submit("et après-demain ?");
    // The message sent is the last exchange now: the one before goes.
    expect(bubbles()).toEqual(["et après-demain ?"]);
    expect(left().querySelector(".present-brain")).toBeNull();
    expect(left().querySelector(".exchange-dots")).not.toBeNull();
    await act(async () =>
      FakeSource.emit("exchange", {
        lines: [
          { seq: 7, role: "user", text: "et après-demain ?", time: at(0) },
          { seq: 8, role: "brain", text: "Du soleil.", time: at(0) },
        ],
        earlier: true,
      }),
    );
    expect(bubbles()).toEqual(["et après-demain ?"]);
    expect(left().querySelector(".present-brain")!.textContent).toBe("Du soleil.");
  });
});

describe("earlier messages", () => {
  const exchange = {
    lines: [
      { seq: 9, role: "user", text: "et après-demain ?", time: at(1) },
      { seq: 10, role: "brain", text: "Du soleil.", time: at(0) },
    ],
    earlier: true,
  };
  const earlierButton = () => left().querySelector<HTMLButtonElement>(".present-earlier");

  it("shows the button once the user scrolls up the column; each click brings the lines before", async () => {
    earlierPages[9] = { lines: [{ seq: 7, role: "user", text: "quel temps demain ?", time: at(5) }, { seq: 8, role: "brain", text: "Demain, il pleut.", time: at(5) }], more: true };
    earlierPages[7] = { lines: [{ seq: 2, role: "user", text: "bonjour", time: at(60) }], more: false };
    await open();
    await act(async () => FakeSource.emit("exchange", exchange));
    expect(earlierButton()).toBeNull(); // not before scrolling up
    await act(async () => void left().dispatchEvent(new WheelEvent("wheel", { deltaY: 40, bubbles: true })));
    expect(earlierButton()).toBeNull(); // down is no asking
    await act(async () => void left().dispatchEvent(new WheelEvent("wheel", { deltaY: -40, bubbles: true })));
    expect(earlierButton()!.textContent).toBe("Messages d'avant");

    await act(async () => earlierButton()!.click());
    await settle();
    expect(posts.some((p) => p.url === "/api/espace/earlier?before=9")).toBe(true);
    expect(bubbles()).toEqual(["quel temps demain ?", "et après-demain ?"]);
    // The newest line is still the large one; the arrival sits between.
    expect(left().querySelector(".present-brain.latest")!.textContent).toBe("Du soleil.");

    await act(async () => earlierButton()!.click());
    await settle();
    expect(posts.some((p) => p.url === "/api/espace/earlier?before=7")).toBe(true);
    expect(bubbles()).toEqual(["bonjour", "quel temps demain ?", "et après-demain ?"]);
    expect(earlierButton()).toBeNull(); // nothing more before

    // The next exchange: back to it alone.
    await act(async () =>
      FakeSource.emit("exchange", { lines: [{ seq: 11, role: "user", text: "merci", time: at(0) }], earlier: true }),
    );
    expect(bubbles()).toEqual(["merci"]);
    expect(earlierButton()).toBeNull();
  });

  it("shows it on a scroll up, a touch or a key too; never when nothing comes before", async () => {
    await open();
    await act(async () => FakeSource.emit("exchange", exchange));
    await act(async () => void left().dispatchEvent(new KeyboardEvent("keydown", { key: "PageUp", bubbles: true })));
    expect(earlierButton()).not.toBeNull();

    await act(async () => FakeSource.emit("exchange", { ...exchange, lines: [{ seq: 12, role: "user", text: "encore", time: at(0) }] }));
    expect(earlierButton()).toBeNull();
    const box = left();
    Object.defineProperty(box, "scrollTop", { configurable: true, writable: true, value: 200 });
    Object.defineProperty(box, "scrollHeight", { configurable: true, writable: true, value: 900 });
    await act(async () => void box.dispatchEvent(new Event("scroll")));
    // The content shrank under a view kept at its end: no asking.
    box.scrollTop = 150;
    Object.defineProperty(box, "scrollHeight", { configurable: true, value: 850 });
    await act(async () => void box.dispatchEvent(new Event("scroll")));
    expect(earlierButton()).toBeNull();
    box.scrollTop = 120;
    await act(async () => void box.dispatchEvent(new Event("scroll")));
    expect(earlierButton()).not.toBeNull();

    await act(async () => FakeSource.emit("exchange", { ...exchange, lines: [{ seq: 13, role: "user", text: "et puis", time: at(0) }] }));
    expect(earlierButton()).toBeNull();
    const finger = (type: string, y: number) => {
      const e = new Event(type, { bubbles: true });
      Object.defineProperty(e, "touches", { value: [{ clientY: y }] });
      return e;
    };
    await act(async () => void box.dispatchEvent(finger("touchstart", 100)));
    await act(async () => void box.dispatchEvent(finger("touchmove", 160)));
    expect(earlierButton()).not.toBeNull();

    await act(async () => FakeSource.emit("exchange", { lines: [{ seq: 1, role: "user", text: "le premier", time: at(0) }] }));
    await act(async () => void left().dispatchEvent(new WheelEvent("wheel", { deltaY: -40, bubbles: true })));
    expect(earlierButton()).toBeNull(); // the first message: nothing before
  });
});

describe("a message written with a thread open", () => {
  it("goes about the thread, and shows in the left column, never in the thread's card", async () => {
    await open("thread-3");
    await submit("mets plutôt un formulaire Formspree");
    expect(posts.find((p) => p.url === "/messages")!.form?.get("about")).toBe("surface:thread-3");
    expect(bubbles()).toEqual(["mets plutôt un formulaire Formspree"]);
    expect(left().querySelector(".exchange-dots")).not.toBeNull();
    await act(async () =>
      FakeSource.emit("exchange", {
        lines: [
          { seq: 5, role: "user", text: "mets plutôt un formulaire Formspree", time: at(1) },
          { seq: 6, role: "brain", text: "D'accord : **Formspree** plutôt que mailto.", time: at(0) },
        ],
      }),
    );
    expect(left().querySelector(".present-brain strong")!.textContent).toBe("Formspree");
    expect(card().querySelector(".present-bubble, .present-brain, .exchange-dots")).toBeNull();
    expect(card().textContent).not.toContain("Formspree");
  });
});

describe("what the page places", () => {
  const user = (seq: number) => ({ seq, role: "user" as const, text: "m" + seq, time: "2026-11-15T10:00:00Z" });
  const brain = (seq: number) => ({ seq, role: "brain" as const, text: "r" + seq, time: "2026-11-15T10:00:00Z" });

  it("knows when the message sent has landed", () => {
    const sent = { text: "salut", after: 4 };
    expect(landed({ lines: [brain(4)] }, sent)).toBe(false);
    expect(landed({ lines: [user(5)] }, sent)).toBe(true);
  });

  it("shows the message sent alone until it lands, then the exchange; Eriphe at work while nothing answers", () => {
    const x = { lines: [user(1), brain(2)] };
    expect(present(x, null)).toEqual({ items: [{ kind: "line", line: user(1) }, { kind: "line", line: brain(2) }], writing: false });
    const sent = present(x, { text: "et ?", after: 2 });
    expect(sent.items.map((i) => (i.kind === "line" ? i.line.text : ""))).toEqual(["et ?"]);
    expect(sent.writing).toBe(true);
    const drafting = present({ lines: [user(3)], draft: "Alors" }, { text: "m3", after: 2 });
    expect(drafting.items.map((i) => i.kind)).toEqual(["line", "draft"]);
    expect(drafting.writing).toBe(false);
  });
});

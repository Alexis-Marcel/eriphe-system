// @vitest-environment jsdom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import * as data from "./data";
import { Espace } from "./Espace";
import { landed, shown } from "./Exchange";
import { WordsContext, makeT } from "./i18n";
import { localURL } from "./presence";

// The workspace is a full home: it tells eriphe-core the user is there
// (presence), shows the notifications routed to it, and the conversation's
// last exchange, Eriphe's reply as it comes.

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
  "espace.exchange": "Dernier échange",
  "espace.exchange.you": "Toi",
  "espace.exchange.writing": "Eriphe écrit…",
  "espace.exchange.all": "Toute la conversation",
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
  });
});

describe("Eriphe's replies in the workspace", () => {
  it("keeps the last exchange folded on arrival, shows a message sent at once, then Eriphe's reply as it comes", async () => {
    await draw();
    await act(async () =>
      FakeSource.emit("exchange", {
        lines: [
          { seq: 3, role: "user", text: "avant", time: "" },
          { seq: 4, role: "brain", text: "ok", time: "" },
        ],
      }),
    );
    expect(el.querySelector(".exchange")).not.toBeNull();
    expect(el.querySelector(".exchange-body")).toBeNull(); // folded
    await act(async () => el.querySelector<HTMLButtonElement>(".exchange-toggle")!.click());
    expect(el.querySelector(".exchange-brain")!.textContent).toBe("ok");
    await act(async () => el.querySelector<HTMLButtonElement>(".exchange-toggle")!.click());

    const input = el.querySelector<HTMLInputElement>(".eriphe-input input")!;
    input.value = "quel temps demain ?";
    await act(async () => void el.querySelector("form")!.dispatchEvent(new Event("submit", { bubbles: true, cancelable: true })));
    await settle();
    const sent = posts.find((p) => p.url === "/messages")!;
    expect(sent.form?.get("text")).toBe("quel temps demain ?");
    expect(sent.headers["X-CSRF-Token"]).toBe("tok");
    // Shown at once, with Eriphe at work.
    expect(el.querySelector(".exchange-user")!.textContent).toContain("quel temps demain ?");
    expect(el.querySelector(".exchange-dots")).not.toBeNull();
    expect(el.querySelector(".exchange-toggle")!.textContent).toBe("Eriphe écrit…");

    // The stream: the message landed, Eriphe writes, then its reply.
    await act(async () => FakeSource.emit("exchange", { lines: [{ seq: 5, role: "user", text: "quel temps demain ?", time: "" }], thinking: true }));
    expect(el.querySelectorAll(".exchange-user")).toHaveLength(1);
    await act(async () => FakeSource.emit("exchange", { lines: [{ seq: 5, role: "user", text: "quel temps demain ?", time: "" }], draft: "Demain, il" }));
    expect(el.querySelector(".exchange-brain.draft")!.textContent).toBe("Demain, il");
    await act(async () =>
      FakeSource.emit("exchange", {
        lines: [
          { seq: 5, role: "user", text: "quel temps demain ?", time: "" },
          { seq: 6, role: "brain", text: "Demain, il **pleut**. <img src=x onerror=alert(1)>", time: "" },
        ],
      }),
    );
    const reply = el.querySelector(".exchange-brain:not(.draft)")!;
    expect(reply.querySelector("strong")!.textContent).toBe("pleut");
    expect(reply.querySelector("img")).toBeNull(); // Markdown, never HTML
    expect(el.querySelector(".exchange-dots")).toBeNull();
    expect(el.querySelector(".exchange-toggle")!.textContent).toBe("Dernier échange");
    expect(el.querySelector<HTMLAnchorElement>(".exchange-all")!.getAttribute("href")).toBe("/conversation");

    // Folded by the user, unfolded again by Eriphe's next line.
    await act(async () => el.querySelector<HTMLButtonElement>(".exchange-toggle")!.click());
    expect(el.querySelector(".exchange-body")).toBeNull();
    await act(async () =>
      FakeSource.emit("exchange", {
        lines: [
          { seq: 5, role: "user", text: "quel temps demain ?", time: "" },
          { seq: 6, role: "brain", text: "Demain, il **pleut**.", time: "" },
          { seq: 7, role: "error", time: "" },
        ],
      }),
    );
    expect(el.querySelector(".exchange-error")).not.toBeNull();
  });

  it("knows when the message sent has landed", () => {
    const sent = { text: "salut", after: 4 };
    const before = { lines: [{ seq: 4, role: "brain" as const, text: "ok", time: "" }] };
    const after = { lines: [{ seq: 5, role: "user" as const, text: "salut", time: "" }] };
    expect(landed(before, sent)).toBe(false);
    expect(shown(before, sent).lines[0].text).toBe("salut");
    expect(landed(after, sent)).toBe(true);
    expect(shown(null, null).lines).toEqual([]);
  });
});

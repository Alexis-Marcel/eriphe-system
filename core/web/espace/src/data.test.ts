// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { createAdapter, type ActionMessage, type Adapter, type Message } from "./a2ui/adapter";
import { CATALOG } from "./a2ui/catalog";
import * as data from "./data";

// A stand-in for the browser's EventSource: the test plays eriphe-core.
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
  emit(type: string, payload: unknown) {
    const e = new MessageEvent(type, { data: JSON.stringify(payload) });
    for (const h of this.listeners.get(type) ?? []) h(e);
  }
  static open() {
    return FakeSource.all.filter((s) => !s.closed);
  }
}

const boot: data.Boot = { lang: "fr", name: "Eriphe", csrf: "tok", strings: {}, live: true };

const create = (id: string, text: string): Message => ({
  version: "1.0",
  createSurface: { surfaceId: id, catalogId: CATALOG.id, components: [{ id: "root", component: "Text", text }] },
});

let posts: { url: string; headers: Record<string, string>; body: unknown }[];
let adapter: Adapter;

beforeEach(() => {
  FakeSource.all = [];
  posts = [];
  vi.stubGlobal("EventSource", FakeSource);
  vi.stubGlobal("fetch", async (url: string, init: RequestInit) => {
    posts.push({ url, headers: init.headers as Record<string, string>, body: JSON.parse(String(init.body)) });
    return new Response(null, { status: 204 });
  });
  adapter = createAdapter("fr");
});

afterEach(() => {
  adapter.dispose();
  vi.unstubAllGlobals();
});

const ids = () => adapter.surfaces().map((s) => s.id);

describe("the surfaces' stream", () => {
  it("applies the a2ui events in order, and starts again on a reset", () => {
    const stop = data.stream(boot, adapter);
    const [es] = FakeSource.open();
    expect(es.url).toBe("/events?a2ui=1");

    es.emit("a2ui", { seq: 4, reset: true, messages: [create("one", "Un")] });
    expect(ids()).toEqual(["one"]);
    es.emit("a2ui", { seq: 6, messages: [create("two", "Deux"), { version: "1.0", deleteSurface: { surfaceId: "one" } }] });
    expect(ids()).toEqual(["two"]);
    // An event already applied is not applied again.
    es.emit("a2ui", { seq: 5, messages: [create("old", "Vieux")] });
    expect(ids()).toEqual(["two"]);

    // A reset (a new connection, or the stream fell behind): what the page
    // had goes, the snapshot comes, its seq counts from where core is.
    es.emit("a2ui", { seq: 0, reset: true, messages: [create("three", "Trois")] });
    expect(ids()).toEqual(["three"]);
    es.emit("a2ui", { seq: 1, messages: [create("four", "Quatre")] });
    expect(ids()).toEqual(["three", "four"]);
    es.emit("a2ui", { seq: 0, reset: true, messages: [] });
    expect(ids()).toEqual([]);

    stop();
    expect(FakeSource.open()).toEqual([]);
    expect(posts).toEqual([]);
  });

  it("shares one stream with what follows the proposals, opened again to get the snapshot", () => {
    const moved = vi.fn();
    const stopFollow = data.followProposals(moved);
    expect(FakeSource.open().map((s) => s.url)).toEqual(["/events"]);
    const stop = data.stream(boot, adapter);
    const open = FakeSource.open();
    expect(open.map((s) => s.url)).toEqual(["/events?a2ui=1"]);
    open[0].emit("a2ui", { seq: 1, reset: true, messages: [create("one", "Un")] });
    expect(ids()).toEqual(["one"]);
    // The proposals' listener moved to the new stream.
    open[0].emit("proposals", { refs: ["rule:7"] });
    expect(moved).toHaveBeenCalledWith({ refs: ["rule:7"], all: false });
    stop();
    expect(FakeSource.open()).toHaveLength(1);
    stopFollow();
    expect(FakeSource.open()).toEqual([]);
  });

  it("sends the user's actions and the render errors back", async () => {
    let fire: (m: ActionMessage) => void = () => {};
    const wired: Adapter = { ...adapter, onAction: (cb) => ((fire = cb), () => {}) };
    const stop = data.stream(boot, wired);
    const [es] = FakeSource.open();

    const act: ActionMessage = {
      version: "1.0",
      action: { name: "open", surfaceId: "one", sourceComponentId: "root", timestamp: "2026-11-12T08:00:00Z", context: {} },
    };
    fire(act);
    // A message about a surface the page does not have: the engine refuses
    // it, the error goes to its author.
    es.emit("a2ui", { seq: 2, messages: [{ version: "1.0", updateDataModel: { surfaceId: "ghost", path: "/x", value: 1 } }] });
    await Promise.resolve();

    expect(posts.map((p) => p.url)).toEqual(["/api/espace/action", "/api/espace/error"]);
    expect(posts[0].body).toEqual(act);
    expect(posts[0].headers["X-CSRF-Token"]).toBe("tok");
    expect(posts[0].headers["Content-Type"]).toBe("application/json");
    expect(posts[1].body).toMatchObject({ version: "1.0", error: { surfaceId: "ghost" } });
    stop();
  });

  it("sends a Proposal's yes or no as an answer action on its surface", async () => {
    expect(await data.answerProposal({ surfaceId: "s1", componentId: "p", ref: "rule:7", yes: false })).toBe(true);
    expect(posts).toHaveLength(1);
    expect(posts[0].url).toBe("/api/espace/action");
    expect(posts[0].headers["X-CSRF-Token"]).toBe("tok");
    expect(posts[0].body).toMatchObject({
      version: "1.0",
      action: { name: "answer", surfaceId: "s1", sourceComponentId: "p", context: { proposal: "rule:7", answer: "no" } },
    });
  });
});

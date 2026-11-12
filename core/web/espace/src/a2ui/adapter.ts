import { MessageProcessor, type ActionPayload, type ComponentApi, type SurfaceModel } from "@a2ui/web_core/v1_0";
import type { Lang } from "../i18n";
import { catalog } from "./catalog";

// The adapter between eriphe-core and the A2UI engine (@a2ui/web_core): the
// rest of the page knows only this. It feeds the engine the messages core
// sends (createSurface, updateComponents, updateDataModel, deleteSurface)
// and hands back, as A2UI v1.0 messages ready to send, what the user did
// (action) and what went wrong (error). The transport is data.ts's (stream).

// What eriphe-core sends: A2UI v1.0 agent-to-renderer messages.
export type Message = { version: "1.0" } & (
  | { createSurface: { surfaceId: string; catalogId: string; components?: unknown[]; dataModel?: unknown; sendDataModel?: boolean } }
  | { updateComponents: { surfaceId: string; components: unknown[] } }
  | { updateDataModel: { surfaceId: string; path?: string; value?: unknown } }
  | { deleteSurface: { surfaceId: string } }
);

// What the page sends back: A2UI v1.0 renderer-to-agent messages.
export type Action = {
  name: string;
  surfaceId: string;
  sourceComponentId: string;
  timestamp: string;
  context: Record<string, unknown>;
  userMessage?: string;
};
export type ActionMessage = { version: "1.0"; action: Action };

// An error, in one of the two shapes A2UI v1.0 allows (renderer_to_agent.json):
// a validation error names its surface and the path at fault; any other
// names its surface.
export type ErrorMessage = {
  version: "1.0";
  error: { code: string; message: string; surfaceId: string; path?: string };
};

const VALIDATION = new Set(["VALIDATION_FAILED", "UNALLOWED_PARENT", "UNALLOWED_CHILD"]);

type Reported = { code?: unknown; message?: unknown; surfaceId?: unknown; path?: unknown };

const str = (v: unknown) => (typeof v === "string" && v ? v : undefined);

// toError keeps of what the engine reports only what the protocol allows;
// surfaceId is the surface of the message being applied, for an error that
// names none.
export function toError(e: Reported, surfaceId: string): ErrorMessage {
  const code = str(e.code) ?? "RENDER_FAILED";
  const message = str(e.message) ?? code;
  const sid = str(e.surfaceId) ?? surfaceId;
  if (VALIDATION.has(code)) return { version: "1.0", error: { code, surfaceId: sid, path: str(e.path) ?? "/", message } };
  return { version: "1.0", error: { code, message, surfaceId: sid } };
}

// The surface a message is about: its one key names it.
function surfaceOf(m: unknown): string {
  for (const v of m && typeof m === "object" ? Object.values(m) : []) {
    const id = v && typeof v === "object" ? str((v as { surfaceId?: unknown }).surfaceId) : undefined;
    if (id) return id;
  }
  return "";
}

export type Surface = SurfaceModel<ComponentApi>;

export interface Adapter {
  // applyMessages feeds the engine; a message it refuses is reported to
  // onError, and the others still apply.
  applyMessages(msgs: Message | Message[] | unknown[]): void;
  onAction(cb: (m: ActionMessage) => void): () => void;
  onError(cb: (m: ErrorMessage) => void): () => void;
  // The surfaces, in the order they were created; subscribe calls back when
  // one is created or deleted.
  surfaces(): Surface[];
  subscribe(cb: () => void): () => void;
  dispose(): void;
}

function listeners<T>() {
  const set = new Set<(v: T) => void>();
  return {
    add(cb: (v: T) => void) {
      set.add(cb);
      return () => void set.delete(cb);
    },
    emit(v: T) {
      for (const cb of set) cb(v);
    },
  };
}

// createAdapter makes the adapter for the user's language: the catalog's
// functions write numbers and dates in it.
export function createAdapter(lang: Lang = "fr"): Adapter {
  const actions = listeners<ActionMessage>();
  const errors = listeners<ErrorMessage>();
  const changes = listeners<void>();

  const toAction = (a: ActionPayload): ActionMessage => {
    const { name, surfaceId, sourceComponentId, timestamp, context, userMessage } = a;
    return { version: "1.0", action: { name, surfaceId, sourceComponentId, timestamp, context, ...(userMessage ? { userMessage } : {}) } };
  };

  // An error is told once while its cause lasts: each render of a surface
  // resolves it anew, and may report the same one again. A message to the
  // surface may end the cause: what follows it counts anew.
  const told = new Map<string, Set<string>>();
  const report = (e: ErrorMessage) => {
    const key = [e.error.code, e.error.path ?? "", e.error.message].join("\n");
    const seen = told.get(e.error.surfaceId) ?? new Set<string>();
    told.set(e.error.surfaceId, seen);
    if (seen.has(key)) return;
    seen.add(key);
    errors.emit(e);
  };

  let current = ""; // the surface of the message being applied
  const processor = new MessageProcessor([catalog(lang)], (a) => actions.emit(toAction(a)), {
    version: "1.0",
    reportErrorsOnly: true,
    errorHandler: (e) => report(toError(e.error as Reported, current)),
  });

  // A surface's own errors (a component of a type the catalog does not
  // have, a function that fails) come from the surface, not from processing.
  const subs = new Map<string, { unsubscribe(): void }>();
  let list: Surface[] = [];
  const sync = () => {
    list = [...processor.getSurfaces().values()];
    changes.emit();
  };
  const created = processor.onSurfaceCreated((s) => {
    subs.set(s.id, s.onError.subscribe((e) => report(toError(e, s.id))));
    sync();
  });
  const deleted = processor.onSurfaceDeleted((id) => {
    subs.get(id)?.unsubscribe();
    subs.delete(id);
    sync();
  });

  return {
    applyMessages(msgs) {
      // One at a time: a message the engine refuses does not take the
      // others with it.
      for (const m of Array.isArray(msgs) ? msgs : [msgs]) {
        current = surfaceOf(m);
        told.delete(current);
        try {
          processor.processMessages(m as never);
        } catch (e) {
          report(toError({ code: "VALIDATION_FAILED", message: e instanceof Error ? e.message : String(e) }, current));
        }
      }
      current = "";
    },
    onAction: actions.add,
    onError: errors.add,
    surfaces: () => list,
    subscribe: changes.add,
    dispose() {
      created.unsubscribe();
      deleted.unsubscribe();
      for (const s of subs.values()) s.unsubscribe();
      processor.dispose();
    },
  };
}

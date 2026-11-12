import { MessageProcessor, type ActionPayload, type ComponentApi, type SurfaceModel } from "@a2ui/web_core/v1_0";
import { catalog } from "./catalog";

// The adapter between eriphe-core and the A2UI engine (@a2ui/web_core): the
// rest of the page knows only this. It feeds the engine the messages core
// sends (createSurface, updateComponents, updateDataModel, deleteSurface)
// and hands back, as A2UI v1.0 messages ready to send, what the user did
// (action) and what went wrong (error). The transport is data.ts's (#419).

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
export type ErrorMessage = {
  version: "1.0";
  error: { code: string; message: string; surfaceId?: string; path?: string; [k: string]: unknown };
};

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

export function createAdapter(): Adapter {
  const actions = listeners<ActionMessage>();
  const errors = listeners<ErrorMessage>();
  const changes = listeners<void>();

  const toAction = (a: ActionPayload): ActionMessage => {
    const { name, surfaceId, sourceComponentId, timestamp, context, userMessage } = a;
    return { version: "1.0", action: { name, surfaceId, sourceComponentId, timestamp, context, ...(userMessage ? { userMessage } : {}) } };
  };
  const toError = (e: { code: string; message: string; surfaceId?: string; path?: string }): ErrorMessage => ({
    version: "1.0",
    error: { ...e, code: e.code, message: e.message },
  });

  const processor = new MessageProcessor([catalog()], (a) => actions.emit(toAction(a)), {
    version: "1.0",
    reportErrorsOnly: true,
    errorHandler: (e) => errors.emit(toError(e.error as ErrorMessage["error"])),
  });

  // A surface's own errors (a function that fails, a child that names no
  // component) come from the surface, not from processing.
  const subs = new Map<string, { unsubscribe(): void }>();
  let list: Surface[] = [];
  const sync = () => {
    list = [...processor.getSurfaces().values()];
    changes.emit();
  };
  const created = processor.onSurfaceCreated((s) => {
    subs.set(s.id, s.onError.subscribe((e) => errors.emit(toError({ ...e, surfaceId: e.surfaceId ?? s.id }))));
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
        try {
          processor.processMessages(m as Message as never);
        } catch (e) {
          errors.emit(toError({ code: "INVALID_MESSAGE", message: e instanceof Error ? e.message : String(e) }));
        }
      }
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

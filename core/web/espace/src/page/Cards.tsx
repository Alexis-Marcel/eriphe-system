import { useEffect, useSyncExternalStore } from "react";
import { useAdapter } from "../a2ui/adapter";
import { CATALOG } from "../a2ui/catalog";
import { HomeNode } from "../a2ui/proposals";
import { SurfaceView } from "../a2ui/Surfaces";
import { useWords } from "../i18n";

// Cards are cards that wait for the user, as the catalog's Proposal shows
// them: code's own surface, each answered by its card's Oui or Non. A
// node's page shows those it waits on (home: that node); the console,
// those no node holds.
export function Cards({ surface, refs, home }: { surface: string; refs: string[]; home?: string }) {
  const { lang } = useWords();
  const adapter = useAdapter(lang);
  const key = refs.join(" ");
  useEffect(() => {
    const list = key ? key.split(" ") : [];
    adapter.applyMessages(adapter.surfaces().map((s) => ({ version: "1.0" as const, deleteSurface: { surfaceId: s.id } })));
    adapter.applyMessages([
      {
        version: "1.0",
        createSurface: {
          surfaceId: surface,
          catalogId: CATALOG.id,
          components: [{ id: "root", component: "Column", children: list.map((_, i) => `c${i}`) }, ...list.map((ref, i) => ({ id: `c${i}`, component: "Proposal", proposalId: ref }))],
          dataModel: {},
          metadata: { extensions: { eriphe: { author: "core" } } },
        },
      },
    ]);
  }, [adapter, key, surface]);
  const s = useSyncExternalStore(adapter.subscribe, adapter.surfaces).find((x) => x.id === surface);
  return s ? (
    <HomeNode.Provider value={home}>
      <SurfaceView surface={s} />
    </HomeNode.Provider>
  ) : null;
}

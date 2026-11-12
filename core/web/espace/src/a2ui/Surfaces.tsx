import { memo, useCallback, useMemo, useSyncExternalStore } from "react";
import type { NodeBuildChild, ReactCatalogComponent, ReactComponentImplementation } from "@a2ui/react";
import { NodeResolver, effect, getValue, isComponentNode, peekValue, type ComponentNode } from "@a2ui/web_core/v1_0";
import type { Adapter, Surface } from "./adapter";

// The A2UI surfaces eriphe-core sends, rendered with our components.
//
// The engine (@a2ui/web_core) keeps each surface: its components, its data,
// the bindings, the templates, the actions. @a2ui/react gives each of our
// components its props, resolved and kept fresh (createComponentImplementation).
// The tree itself is walked here, not by @a2ui/react's A2uiSurface: that one
// wraps every component in a custom element whose style it sets (display:
// contents), and pulls in the Lit runtime its basic catalog needs. Here a
// component renders as its own markup, nothing around it.

type Node = ComponentNode<ReactCatalogComponent>;

const NodeView = memo(function NodeView({ node }: { node: Node }) {
  // A child id the engine did not resolve (a property the catalog does not
  // mark as a child) shows nothing, and is reported through the surface's
  // errors (the adapter's onError, which tells it once).
  const build: NodeBuildChild = useCallback(
    (child, basePath) => {
      if (isComponentNode(child)) return <NodeView key={child.id} node={child as Node} />;
      const surface = node.context?.dataContext.surface;
      const message = `Unresolved child reference '${child}' at '${basePath ?? node.dataPath}'.`;
      queueMicrotask(() => void surface?.dispatchError({ code: "UNRESOLVED_CHILD_REFERENCE", message }));
      return null;
    },
    [node],
  );
  const impl = node.impl as ReactComponentImplementation | undefined;
  if (node.isPlaceholder || node.disposed || !node.context || !impl?.view) return null;
  const View = impl.view;
  return <View node={node} buildChild={build} />;
});

// The root of one surface: the engine's resolver, made when React subscribes
// and disposed when it stops, turns the surface into a tree of nodes.
function SurfaceView({ surface }: { surface: Surface }) {
  const box = useMemo(() => ({ resolver: undefined as NodeResolver | undefined }), [surface]);
  const subscribe = useCallback(
    (onChange: () => void) => {
      const resolver = new NodeResolver(surface);
      box.resolver = resolver;
      const stop = effect(() => {
        getValue(resolver.rootNode);
        onChange();
      });
      return () => {
        stop();
        resolver.dispose();
        if (box.resolver === resolver) box.resolver = undefined;
      };
    },
    [surface, box],
  );
  const root = useSyncExternalStore(subscribe, () => (box.resolver ? peekValue(box.resolver.rootNode) : undefined)) as Node | undefined;
  return root ? <NodeView node={root} /> : null;
}

// Surfaces renders every surface of the adapter, each in its section; where
// they go on the workspace is the page's (#422).
export function Surfaces({ adapter }: { adapter: Adapter }) {
  const surfaces = useSyncExternalStore(adapter.subscribe, adapter.surfaces);
  return surfaces.map((s) => (
    <section key={s.id} className="a2-surface" data-surface={s.id}>
      <SurfaceView surface={s} />
    </section>
  ));
}

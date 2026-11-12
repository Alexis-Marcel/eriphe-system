import type { ReactNode } from "react";
import { createComponentImplementation, type ReactComponentImplementation } from "@a2ui/react";
import { Catalog, CommonSchemas } from "@a2ui/web_core/v1_0";
import { z } from "zod";

// The workspace's A2UI catalog, as the page renders it: shapes, never tools.
// eriphe-core owns the catalog (its JSON Schema, which validates what a
// model writes and tells it what it may use); the page implements each
// component with React, under the workspace's CSP: classes only, no style
// attribute, no <style>. The full set is written in #420; these few prove
// the path.
//
// CATALOG is the one place that names the catalog: its id, which a
// surface's createSurface gives, and where eriphe-core serves its document.
export const CATALOG = {
  id: "urn:eriphe:a2ui:workspace:v1",
  url: "/espace/a2ui/catalog/v1.json",
} as const;

const Justify = z.enum(["start", "center", "end", "spaceBetween"]).optional();
const Align = z.enum(["start", "center", "end", "stretch"]).optional();

// A child list, once resolved: a component id, or the id and the data path
// of one instance of a template.
type Child = string | { id: string; basePath?: string };
type Build = (id: string, basePath?: string) => ReactNode;

function kids(children: unknown, build: Build): ReactNode[] {
  return (Array.isArray(children) ? (children as Child[]) : []).map((c) =>
    typeof c === "string" ? build(c) : build(c.id, c.basePath),
  );
}

const Text = createComponentImplementation(
  {
    name: "Text",
    schema: z.object({
      text: CommonSchemas.DynamicString,
      variant: z.enum(["title", "subtitle", "body", "caption"]).optional(),
    }),
  },
  ({ props }) => <p className={`a2-text a2-text-${props.variant ?? "body"}`}>{props.text}</p>,
);

const box = (dir: "row" | "column") =>
  createComponentImplementation(
    {
      name: dir === "row" ? "Row" : "Column",
      schema: z.object({ children: CommonSchemas.ChildList, justify: Justify, align: Align }),
    },
    ({ props, buildChild }) => (
      <div className={`a2-${dir} a2-justify-${props.justify ?? "start"} a2-align-${props.align ?? "stretch"}`}>
        {kids(props.children, buildChild)}
      </div>
    ),
  );

const List = createComponentImplementation(
  {
    name: "List",
    schema: z.object({
      children: CommonSchemas.ChildList,
      direction: z.enum(["vertical", "horizontal"]).optional(),
      emptyText: CommonSchemas.DynamicString.optional(),
    }),
  },
  ({ props, buildChild }) => {
    const items = kids(props.children, buildChild);
    if (!items.length) return props.emptyText ? <p className="a2-empty">{props.emptyText}</p> : null;
    return (
      <ul className={`a2-list a2-list-${props.direction ?? "vertical"}`}>
        {items.map((item, i) => (
          <li key={i}>{item}</li>
        ))}
      </ul>
    );
  },
);

const Button = createComponentImplementation(
  {
    name: "Button",
    schema: z.object({
      label: CommonSchemas.DynamicString,
      variant: z.enum(["primary", "default", "quiet"]).optional(),
      action: CommonSchemas.Action,
    }),
  },
  ({ props }) => (
    <button type="button" className={`btn a2-button a2-button-${props.variant ?? "default"}`} onClick={() => props.action?.()}>
      {props.label}
    </button>
  ),
);

export const components: ReactComponentImplementation[] = [Text, box("row"), box("column"), List, Button];

export const catalog = () => new Catalog(CATALOG.id, "1.0", components);

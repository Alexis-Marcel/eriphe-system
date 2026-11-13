// The layout of the surfaces, owned by eriphe-core (workspaceLayout in
// workspace_layout.go): the event stream's "layout". A tile is a surface at
// its place on the 12 × 8 grid (col and row from 0, w and h in cells); a
// chip, a surface folded in the strip above it. pinned: the user placed it
// for the session; related: it relates to the surface open (focus);
// closed: the user closed it for the session.
export type TileSize = "s" | "m" | "l" | "xl";
export type Tile = { id: string; title: string; size: TileSize; col: number; row: number; w: number; h: number; why?: string; pinned?: boolean; related?: boolean };
export type FoldedChip = { id: string; title: string; why?: string; closed?: boolean };
export type Layout = {
  eriphe: string;
  by: string;
  ambient: "calm" | "decision" | "urgent";
  focus?: string;
  say?: string; // the brain's line about the surface open (workspace_focus.go)
  focusBy?: string; // "brain": Eriphe put it on stage, answering the user (workspace_front.go)
  previous?: string; // what was on stage before it: a tile reduced next to it
  items: Tile[];
  chips: FoldedChip[];
  moment?: string;
  at?: string;
  missing?: string[];
};

import { HomeNode } from "../a2ui/proposals";
import { Card } from "./Card";

// Cards are cards that wait for the user, drawn by the page from
// eriphe-core's records (Card), each answered by its Oui or ✕. A node's
// page shows those it waits on (home: that node); the console, those no
// node holds.
export function Cards({ surface, refs, home }: { surface: string; refs: string[]; home?: string }) {
  return (
    <HomeNode.Provider value={home}>
      <div className="a2-column a2-justify-start a2-align-stretch" data-surface={surface}>
        {refs.map((ref) => (
          <Card key={ref} refText={ref} surface={surface} />
        ))}
      </div>
    </HomeNode.Provider>
  );
}

import { useState } from "react";
import { avant, cocheArrive, cocheAsk } from "./data/samplemails";
import { store } from "./data/store";
import "./dev.css";

// Prototype « Eriphe coche, tu cliques » (proto/eriphe-coche, PROTO.md):
// the dev server's panel that plays its moments on « Mes mails ». Dev
// only, French only, like the ?physique panel. ?coche=a starts after the
// user's request (A), ?coche=ab with a new mail arrived too (B); ?avant
// plays the same moments as main does them today (cards).

let said: ReturnType<typeof cocheAsk> = null;
export function cocheAskNow() {
  said = cocheAsk() ?? said;
  if (said) store.setState({ orb: said });
}

// cocheStart plays what the URL asks, before the page is read (once: the
// dev's start runs twice in StrictMode, the orb's words are set again).
let started = false;
export function cocheStart() {
  if (started) {
    if (said) store.setState({ orb: said });
    return;
  }
  started = true;
  const asked = new URLSearchParams(location.search).get("coche") ?? "";
  if (asked.includes("a")) cocheAskNow();
  if (asked.includes("b")) cocheArrive();
}

// The shots play B by hand (proto-shots/coche.mjs).
(window as unknown as { cocheArrive: () => void }).cocheArrive = cocheArrive;

const REQUEST = "supprime Backblaze, AWS, SNCF, les newsletters et les pubs, archive les factures déjà payées";

export function CochePanel() {
  const [shut, setShut] = useState(false);
  const [said, setSaid] = useState(() => (new URLSearchParams(location.search).get("coche") ?? "").includes("a"));
  const here = new URLSearchParams(location.search);
  const other = new URLSearchParams(here);
  if (avant) other.delete("avant");
  else other.set("avant", "");
  other.delete("coche");
  const link = `?${other.toString().replace(/avant=(&|$)/, "avant$1")}`;
  return (
    <aside className={`dev-phys dev-coche${shut ? " shut" : ""}`} aria-label="Prototype">
      <header>
        <b>{avant ? "Avant (main)" : "Eriphe coche, tu cliques"}</b>
        <button type="button" onClick={() => setShut(!shut)} aria-expanded={!shut}>
          {shut ? "▸" : "▾"}
        </button>
      </header>
      {!shut && (
        <>
          <p className="dev-coche-said">
            A · Tu dis à Eriphe : <q>{REQUEST}</q>
          </p>
          <div className="dev-phys-row">
            <button
              type="button"
              disabled={said}
              onClick={() => {
                cocheAskNow();
                setSaid(true);
              }}
            >
              {said ? "Demandé" : "Le dire"}
            </button>
            <button type="button" onClick={() => cocheArrive()}>
              B · Un mail arrive
            </button>
          </div>
          <div className="dev-phys-row">
            <a href={link}>{avant ? "Voir le prototype" : "Voir avant (?avant)"}</a>
            <a href={`?${new URLSearchParams({ open: "node-mails", ...(avant ? { avant: "" } : {}) }).toString().replace(/avant=$/, "avant")}`}>Recommencer</a>
          </div>
        </>
      )}
    </aside>
  );
}

import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { App } from "./App";
import { boot } from "./data/api";
import { FailSafe } from "./Fallback";
import { WordsContext, makeT } from "./i18n";
import "./fonts.css";
import "./espace.css";

// The shell (web/templates/espace.html) names where the boot is; the dev
// server's page names none, and the app runs on its fixtures. A boot that
// fails leaves the shell's error state, shown at once (.failed), with the
// way to try again; a page that throws while it draws shows it too.
const root = document.getElementById("root")!;

boot(root.dataset.boot).then(
  (b) => {
    document.documentElement.lang = b.lang;
    createRoot(root).render(
      <StrictMode>
        <WordsContext.Provider value={{ t: makeT(b.strings), lang: b.lang }}>
          <FailSafe>
            <App boot={b} />
          </FailSafe>
        </WordsContext.Provider>
      </StrictMode>,
    );
  },
  (err) => {
    console.error("espace:", err);
    root.querySelector(".espace-loading")?.classList.add("failed");
  },
);

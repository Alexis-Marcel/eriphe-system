import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { boot } from "./data";
import { Espace } from "./Espace";
import { FailSafe } from "./Fallback";
import { WordsContext, makeT } from "./i18n";
import "./fonts.css";
import "./espace.css";

// The shell (web/templates/espace.html) names where the boot is; the dev
// server's page names none, and the app runs on its fixtures. A boot that
// fails leaves the shell's fallback, shown at once (.failed): the way to
// the conversation; a page that throws while it draws shows it too.
const root = document.getElementById("root")!;

boot(root.dataset.boot).then(
  (b) => {
    document.documentElement.lang = b.lang;
    createRoot(root).render(
      <StrictMode>
        <WordsContext.Provider value={{ t: makeT(b.strings), lang: b.lang }}>
          <FailSafe csrf={b.csrf}>
            <Espace boot={b} />
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

import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { boot } from "./data";
import { Espace } from "./Espace";
import { WordsContext, makeT } from "./i18n";
import "./espace.css";

// The shell (web/templates/espace.html) names where the boot is; the dev
// server's page names none, and the app runs on its fixtures.
const root = document.getElementById("root")!;

boot(root.dataset.boot).then((b) => {
  document.documentElement.lang = b.lang;
  createRoot(root).render(
    <StrictMode>
      <WordsContext.Provider value={{ t: makeT(b.strings), lang: b.lang }}>
        <Espace boot={b} />
      </WordsContext.Provider>
    </StrictMode>,
  );
});

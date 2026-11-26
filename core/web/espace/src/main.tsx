import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { App } from "./App";
import { boot, type Boot } from "./data/api";
import type { Dev } from "./dev";
import { FailSafe } from "./Fallback";
import { WordsContext, makeT } from "./i18n";
import "./fonts.css";
import "./espace.css";
import "./blocks.css";

// The shell (web/templates/espace.html) names where the boot is; the dev
// server's page names none, and the app runs on its made-up workspace
// (dev.tsx, imported on the dev server only: the build leaves it out). A
// boot that fails leaves the shell's error state, shown at once (.failed),
// with the way to try again; a page that throws while it draws shows it too.
const root = document.getElementById("root")!;

async function start(): Promise<{ b: Boot; dev?: Dev }> {
  const url = root.dataset.boot;
  if (url) return { b: await boot(url) };
  if (!import.meta.env.DEV) throw new Error("no boot");
  const m = await import("./dev");
  const b = m.devBoot();
  return { b, dev: m.dev(b.lang) };
}

start().then(
  ({ b, dev }) => {
    document.documentElement.lang = b.lang;
    createRoot(root).render(
      <StrictMode>
        <WordsContext.Provider value={{ t: makeT(b.strings), lang: b.lang }}>
          <FailSafe>
            <App boot={b} dev={dev} />
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

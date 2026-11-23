/// <reference types="vitest/config" />
import { defineConfig, type Plugin } from "vite";
import react from "@vitejs/plugin-react";

// Prototype « belles vues » (PROTO.md): the dev server on 5318, with
// eriphe-core's /espace/media faked: it draws a placeholder picture
// (a garment, a dish) from the name in ?url=, so the page's Image and the
// prototype's Gallery load real <img>s, with no outside host.

const COLORS: Record<string, string> = {
  bleu: "#4a6fa5", marine: "#2b3a55", blanc: "#ecebe6", noir: "#2a2a2d", gris: "#8b8d93", beige: "#cdb894", vert: "#5d8a66",
  rouge: "#b4443c", camel: "#b48a52", rose: "#d99aa5", jaune: "#e2b84a", brun: "#6e4b32", orange: "#d9793a", creme: "#efe3c8",
};

const SHAPES: Record<string, string> = {
  // garments, drawn on a 100×120 box
  haut: "M30 18 L42 12 Q50 20 58 12 L70 18 L86 36 L76 46 L70 40 L70 104 L30 104 L30 40 L24 46 L14 36 Z",
  pull: "M28 18 L42 12 Q50 22 58 12 L72 18 L90 70 L80 74 L70 44 L70 104 L30 104 L30 44 L20 74 L10 70 Z",
  bas: "M32 14 L68 14 L72 106 L56 106 L50 46 L44 106 L28 106 Z",
  veste: "M28 16 L44 12 L50 40 L56 12 L72 16 L90 74 L80 78 L72 48 L72 106 L28 106 L28 48 L20 78 L10 74 Z",
  chaussure: "M14 70 L14 52 Q30 50 40 40 L52 44 Q58 60 86 66 Q94 70 92 80 L14 80 Z",
  robe: "M38 12 L62 12 L60 34 L82 106 L18 106 L40 34 Z",
};

function picture(name: string): string {
  const parts = name.replace(/\.svg$/, "").split(/[/-]/);
  const color = parts.map((p) => COLORS[p]).find(Boolean) ?? "#9aa0a8";
  const shape = parts.map((p) => SHAPES[p]).find(Boolean);
  if (parts[0] === "recette") {
    // a dish: a plate seen from above, three soft heaps
    const hues = [color, "#7fa46a", "#e0b04f"];
    return `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 160 120"><rect width="160" height="120" fill="#e9e4da"/><circle cx="80" cy="60" r="46" fill="#fbfaf7"/><circle cx="80" cy="60" r="36" fill="#f1eee8"/><circle cx="70" cy="54" r="17" fill="${hues[0]}"/><circle cx="92" cy="66" r="12" fill="${hues[1]}"/><circle cx="84" cy="46" r="8" fill="${hues[2]}"/></svg>`;
  }
  return `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 100 120"><rect width="100" height="120" fill="#eeece8"/><path d="${shape ?? SHAPES.haut}" fill="${color}" stroke="rgb(0 0 0 / 0.18)" stroke-width="1.2" stroke-linejoin="round"/></svg>`;
}

const media: Plugin = {
  name: "proto-media",
  configureServer(server) {
    server.middlewares.use("/espace/media", (req, res) => {
      const u = new URL(req.url ?? "", "http://x").searchParams.get("url") ?? "";
      res.setHeader("Content-Type", "image/svg+xml");
      res.setHeader("Cache-Control", "max-age=3600");
      res.end(picture(u));
    });
  },
};

export default defineConfig({
  plugins: [react(), media],
  server: { port: 5318, strictPort: true },
});

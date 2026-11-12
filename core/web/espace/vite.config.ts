/// <reference types="vitest/config" />
import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

// The workspace page /espace. eriphe-core embeds the built files
// (../static/espace) and serves them from its own HTML shell
// (web/templates/espace.html), so the build has one entry, a script, and
// fixed names: the shell adds ?v=<assetVersion> for the browser's cache.
// No file may start with "_" or ".": go:embed would skip it.

// The workspace's Content-Security-Policy (web.go, headers): the preview
// serves the build with it (npm run preview, on preview.html), to check that
// nothing inline is needed.
const csp =
  "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data: blob:; " +
  "connect-src 'self'; font-src 'self'; manifest-src 'self'; worker-src 'self'; object-src 'none'; " +
  "base-uri 'none'; form-action 'self'; frame-ancestors 'none'";

export default defineConfig({
  plugins: [react()],
  build: {
    outDir: "../static/espace",
    emptyOutDir: true,
    assetsDir: "",
    modulePreload: false,
    rolldownOptions: {
      input: "src/main.tsx",
      output: {
        entryFileNames: "espace.js",
        chunkFileNames: "espace-[name].js",
        assetFileNames: "espace[extname]",
      },
    },
  },
  server: { port: 5300, strictPort: true },
  preview: { port: 5301, strictPort: true, headers: { "Content-Security-Policy": csp } },
  test: { environment: "node", include: ["src/**/*.test.ts"] },
});

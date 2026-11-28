// @vitest-environment jsdom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { NodePage } from "../data/api";
import { makeT, WordsContext } from "../i18n";
import strings from "../strings.json";
import { Props } from "./Props";
import { MAX_SIDE, pageScale } from "./pdfScale";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

// pdf.js itself is not run here (no canvas in jsdom): the browser test
// draws a real PDF (e2e/attach-pdf.spec.ts). Here, the page shows a
// node's PDF with its viewer and its download link, and its picture as a
// picture.
const shown = vi.fn(async (_data: ArrayBuffer, box: HTMLElement) => {
  const c = document.createElement("canvas");
  c.className = "pg-pdf-page";
  box.append(c);
  return stopped;
});
const stopped = vi.fn();
vi.mock("./pdf", () => ({ showPdf: (d: ArrayBuffer, b: HTMLElement) => shown(d, b) }));

const pdfSrc = "/espace/media?u=media%3An1%2Fmed_1.pdf&s=x";
const page = {
  props: [
    { key: "photo", value: "media:med_2.png", kind: "text", media: [{ src: "/espace/media?u=media%3An1%2Fmed_2.png&s=y", image: true }] },
    { key: "recette", value: "media:med_1.pdf", kind: "text", media: [{ src: pdfSrc, image: false }] },
  ],
} as unknown as NodePage;

describe("a node's PDF in its page", () => {
  let host: HTMLDivElement;
  let root: Root;
  beforeEach(() => {
    host = document.createElement("div");
    document.body.append(host);
    root = createRoot(host);
  });
  afterEach(() => {
    act(() => root.unmount());
    host.remove();
    vi.unstubAllGlobals();
    shown.mockClear();
    stopped.mockClear();
  });

  const mount = async () => {
    const t = makeT(strings.fr as Record<string, string>);
    await act(async () => {
      root.render(
        <WordsContext.Provider value={{ t, lang: "fr" }}>
          <Props page={page} />
        </WordsContext.Provider>,
      );
    });
    await act(async () => {
      await new Promise((r) => setTimeout(r, 0));
    });
  };

  it("draws it from its address, under a link that downloads it", async () => {
    const fetched = vi.fn(async () => new Response(new Uint8Array([37, 80, 68, 70]), { status: 200 }));
    vi.stubGlobal("fetch", fetched);
    await mount();
    expect(fetched).toHaveBeenCalledWith(pdfSrc, { credentials: "same-origin" });
    const fig = host.querySelector("figure.pg-pdf")!;
    expect(fig.getAttribute("data-state")).toBe("shown");
    expect(fig.querySelectorAll("canvas.pg-pdf-page")).toHaveLength(1);
    const link = fig.querySelector("a.pg-file")!;
    expect(link.getAttribute("href")).toBe(pdfSrc);
    expect(link.getAttribute("download")).toBe("recette.pdf");
    expect(link.textContent).toBe("recette · télécharger");
    // the picture stays a picture
    expect(host.querySelector("img.pg-picture")!.getAttribute("alt")).toBe("photo");
    act(() => root.unmount());
    expect(stopped).toHaveBeenCalled();
    root = createRoot(host);
  });

  it("keeps the link alone when the file does not come", async () => {
    vi.stubGlobal("fetch", vi.fn(async () => new Response("no such media", { status: 404 })));
    await mount();
    const fig = host.querySelector("figure.pg-pdf")!;
    expect(fig.getAttribute("data-state")).toBe("failed");
    expect(shown).not.toHaveBeenCalled();
    expect((fig.querySelector(".pg-pdf-pages") as HTMLElement).hidden).toBe(true);
    expect(fig.querySelector("a.pg-file")).not.toBeNull();
  });
});

describe("pageScale", () => {
  it("fills the box on a sharp screen", () => {
    expect(pageScale(595, 700, 2)).toBeCloseTo(1400 / 595);
    expect(pageScale(595, 700, 0.5)).toBeCloseTo(700 / 595); // never under one pixel a CSS pixel
  });
  it("caps the canvas", () => {
    expect(pageScale(100, 4000, 3) * 100).toBe(MAX_SIDE);
  });
  it("draws a page of no width at its own size", () => {
    expect(pageScale(0, 700, 2)).toBe(1);
    expect(pageScale(595, 0, 2)).toBe(1);
  });
});

import { getDocument, GlobalWorkerOptions, type PDFDocumentProxy } from "pdfjs-dist";
import workerSrc from "pdfjs-dist/build/pdf.worker.min.mjs?url";
import { pageScale } from "./pdfScale";

// A node's PDF drawn in its page (PdfView.tsx), with the pdf.js bundled
// here: a chunk of its own, loaded only when a page shows a PDF, its
// worker a file beside it (worker-src 'self'). Each page is drawn to a
// canvas, nothing else: no text layer, no annotation layer, no forms, and
// never the PDF's own JavaScript (pdf.js runs scripts only through a
// scripting manager, which is never made). No WebAssembly: the image
// decoders' JavaScript does it, under the workspace's CSP.
GlobalWorkerOptions.workerSrc = workerSrc;

// A page is drawn as it nears the screen: a long PDF does not hold a
// canvas per page at once.
const near = "800px 0px";

// showPdf draws the PDF's pages into box, one canvas each, the width of
// the box, and answers what stops it (the page closed).
export async function showPdf(data: ArrayBuffer, box: HTMLElement): Promise<() => void> {
  const task = getDocument({ data, useWasm: false, isOffscreenCanvasSupported: false, enableXfa: false });
  let doc: PDFDocumentProxy;
  try {
    doc = await task.promise;
  } catch (e) {
    void task.destroy();
    throw e;
  }
  const width = box.clientWidth || 640;
  const dpr = window.devicePixelRatio || 1;
  const watch = new IntersectionObserver(
    (seen) => {
      for (const s of seen) {
        if (!s.isIntersecting) continue;
        watch.unobserve(s.target);
        void draw(doc, s.target as HTMLCanvasElement, width, dpr);
      }
    },
    { rootMargin: near },
  );
  for (let n = 1; n <= doc.numPages; n++) {
    const page = await doc.getPage(n);
    const vp = page.getViewport({ scale: 1 });
    const c = document.createElement("canvas");
    c.className = "pg-pdf-page";
    c.dataset.page = String(n);
    // its place kept before it is drawn: the page's proportions
    c.style.aspectRatio = `${vp.width} / ${vp.height}`;
    box.append(c);
    watch.observe(c);
  }
  return () => {
    watch.disconnect();
    void task.destroy();
  };
}

async function draw(doc: PDFDocumentProxy, c: HTMLCanvasElement, width: number, dpr: number) {
  try {
    const page = await doc.getPage(Number(c.dataset.page));
    const vp = page.getViewport({ scale: pageScale(page.getViewport({ scale: 1 }).width, width, dpr) });
    c.width = Math.floor(vp.width);
    c.height = Math.floor(vp.height);
    await page.render({ canvas: c, viewport: vp }).promise;
    c.dataset.drawn = "1";
  } catch {
    // the document closed meanwhile, or a page it cannot draw: left blank
  }
}

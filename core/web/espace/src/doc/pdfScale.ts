// The largest a page's canvas may be, in pixels on a side: a poster-sized
// page on a sharp screen does not take the memory of a photo album.
export const MAX_SIDE = 4096;

// pageScale is the scale a PDF page of width pageWidth (in points) is
// drawn at to fill a box boxWidth CSS pixels wide on a screen of pixel
// ratio dpr, its canvas at most MAX_SIDE pixels wide.
export function pageScale(pageWidth: number, boxWidth: number, dpr: number): number {
  if (!(pageWidth > 0) || !(boxWidth > 0)) return 1;
  const px = Math.min(boxWidth * Math.max(dpr, 1), MAX_SIDE);
  return px / pageWidth;
}

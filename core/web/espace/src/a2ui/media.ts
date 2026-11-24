// An image is loaded only through eriphe-core (/espace/media, which fetches
// and checks it): a model's URL is never fetched by the page, so it cannot
// carry data out. Any other address shows what the image is, in words.
export function viaCore(url: string): boolean {
  try {
    const u = new URL(url, location.origin);
    return u.origin === location.origin && u.pathname === "/espace/media" && url.startsWith("/espace/media?");
  } catch {
    return false;
  }
}

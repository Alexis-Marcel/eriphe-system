// Runs before the first paint (loaded without defer): an installed app is
// sized on the large viewport from the start (app.css, html.app), so the
// page does not jump when app.js arrives.
if (matchMedia("(display-mode: standalone)").matches || navigator.standalone === true) {
  document.documentElement.classList.add("app");
}

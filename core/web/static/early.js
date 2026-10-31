// Runs before the first paint (loaded without defer): an installed app is
// sized on the large viewport from the start (app.css, html.app), so the
// page does not jump when app.js arrives.
if (matchMedia("(display-mode: standalone)").matches || navigator.standalone === true) {
  document.documentElement.classList.add("app");
}

// The settings' list (/settings) is the first level on a phone; a wide
// screen has it as the sidebar of every page and opens the first entry
// instead, in place of the list (no step added to the history).
if (location.pathname === "/settings" && matchMedia("(min-width: 861px)").matches) {
  location.replace("/memory");
}

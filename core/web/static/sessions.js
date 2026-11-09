// A work session's page: its live part follows the session's own event
// stream (web_sessions.go). What the user unfolded stays unfolded.
(function () {
  "use strict";

  function swap(box, html) {
    if (box.innerHTML === html) return;
    const open = new Set([...box.querySelectorAll("details[data-keep][open]")].map((d) => d.dataset.keep));
    box.innerHTML = html;
    box.querySelectorAll("details[data-keep]").forEach((d) => { if (open.has(d.dataset.keep)) d.open = true; });
    if (window.htmx) htmx.process(box);
  }

  function connect() {
    const box = document.querySelector("[data-session-live]");
    if (!box || !window.EventSource) return;
    const es = new EventSource(box.dataset.sessionLive);
    es.addEventListener("session", (e) => swap(box, e.data));
    es.onerror = () => {
      if (es.readyState === EventSource.CLOSED) setTimeout(connect, 5000);
    };
    // An answer the page posted brings the same fragment back: keep what
    // was unfolded then too.
    document.body.addEventListener("htmx:beforeSwap", (e) => {
      if (e.detail.target !== box || !e.detail.serverResponse) return;
      e.detail.shouldSwap = false;
      swap(box, e.detail.serverResponse);
    });
  }

  document.addEventListener("DOMContentLoaded", connect);
})();

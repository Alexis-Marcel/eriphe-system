// Settings → Remote access: the live part of the page follows eriphe-core's
// event stream (the login, the approval, HTTPS, then the address).
(function () {
  "use strict";

  function connect() {
    const box = document.querySelector("[data-remote-live]");
    if (!box || !window.EventSource) return;
    const es = new EventSource("/settings/remote/events");
    es.addEventListener("remote", (e) => {
      if (box.innerHTML === e.data) return;
      box.innerHTML = e.data;
      if (window.htmx) htmx.process(box);
    });
    es.onerror = () => {
      if (es.readyState === EventSource.CLOSED) setTimeout(connect, 5000);
    };
  }

  document.addEventListener("DOMContentLoaded", connect);
})();

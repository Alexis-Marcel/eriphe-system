// Eriphe's workspace: the little the server cannot do. htmx sends the
// forms and actions; this file adds the CSRF header, follows the event
// stream, and handles the composer, the task drawer, toasts and copying.

// This tab's id: it tells eriphe-core whether it is in front of the user,
// and gets the notifications routed to it (route.go), by the event stream
// or the answer to its presence.
const eripheTab = Math.random().toString(36).slice(2) + Date.now().toString(36);

// A notification routed to this tab: a banner in the page, no system
// notification. Its link opens it, which is also what "seen" means.
function eripheInApp(notes) {
  "use strict";
  const box = document.getElementById("toasts");
  if (!box || !Array.isArray(notes)) return;
  const lang = document.body.dataset.lang === "en" ? "en" : "fr";
  notes.forEach((n) => {
    const el = document.createElement("div");
    el.className = "toast toast-inapp";
    el.setAttribute("role", "alert");
    const text = document.createElement("span");
    text.textContent = (n.body && (n.body[lang] || n.body.fr)) || "";
    el.appendChild(text);
    if (n.url && n.url.startsWith("/") && !n.url.startsWith("//")) {
      const a = document.createElement("a");
      a.href = n.url;
      a.textContent = box.dataset.open || (lang === "en" ? "Open" : "Voir");
      el.appendChild(a);
    }
    const close = document.createElement("button");
    close.type = "button";
    close.className = "toast-close";
    close.setAttribute("aria-label", lang === "en" ? "Close" : "Fermer");
    close.textContent = "×";
    close.addEventListener("click", () => el.remove());
    el.appendChild(close);
    box.appendChild(el);
    setTimeout(() => el.classList.add("out"), 15000);
    setTimeout(() => el.remove(), 15500);
  });
}

(function () {
  "use strict";

  const $ = (sel, root) => (root || document).querySelector(sel);
  const $$ = (sel, root) => Array.from((root || document).querySelectorAll(sel));
  const csrf = () => ($('meta[name="csrf-token"]') || {}).content || "";

  // Every htmx request carries the session's CSRF token.
  document.addEventListener("htmx:configRequest", (e) => {
    e.detail.headers["X-CSRF-Token"] = csrf();
  });

  // Toasts, sent by the server in HX-Trigger.
  function toast(message, kind) {
    const box = $("#toasts");
    if (!box) return;
    const el = document.createElement("div");
    el.className = "toast toast-" + (kind || "ok");
    el.textContent = message;
    box.appendChild(el);
    setTimeout(() => el.classList.add("out"), 3200);
    setTimeout(() => el.remove(), 3700);
  }
  document.addEventListener("toast", (e) => toast(e.detail.message, e.detail.kind));
  document.addEventListener("htmx:responseError", (e) => {
    const xhr = e.detail.xhr;
    if (xhr.status === 401) return; // HX-Redirect to the login page
    if (updatePoll(e)) return pollFailed(e);
    toast((xhr.responseText || "Erreur").trim().slice(0, 200), "error");
  });
  document.addEventListener("htmx:sendError", (e) => {
    if (updatePoll(e)) return pollFailed(e);
    toast(document.body.dataset.lang === "en" ? "Network error" : "Erreur réseau", "error");
  });
  document.addEventListener("htmx:timeout", (e) => updatePoll(e) && pollFailed(e));

  // A card following an update (data-update-poll, settings.html) asks again,
  // quietly, while eriphe-core does not answer: it restarts when its own
  // machine updates. After 10 minutes without an answer, the card stops
  // asking and says to reload the page.
  const pollGiveUp = 10 * 60 * 1000;
  function updatePoll(e) {
    const elt = e.detail && e.detail.elt;
    return elt && elt.closest ? elt.closest("[data-update-poll]") : null;
  }
  function pollFailed(e) {
    const el = updatePoll(e);
    const since = Number(el.dataset.failSince) || Date.now();
    el.dataset.failSince = since;
    if (Date.now() - since < pollGiveUp) return;
    // htmx polls an element while it is in the page: a copy without its
    // attributes takes its place.
    const still = el.cloneNode(true);
    ["hx-get", "hx-trigger", "hx-swap", "hx-request", "data-update-poll", "data-fail-since"].forEach((a) => still.removeAttribute(a));
    still.classList.remove("htmx-request");
    const state = $(".machine-update > p", still);
    if (state) {
      state.className = "small muted";
      state.textContent = el.dataset.updateLost;
    }
    el.replaceWith(still);
  }

  // Forms marked data-reset are cleared once sent; a permission card fades.
  document.addEventListener("htmx:afterRequest", (e) => {
    const form = e.detail.elt.closest && e.detail.elt.closest("form");
    if (!form || !e.detail.successful) return;
    if (form.hasAttribute("data-reset")) {
      form.reset();
      autosize($("textarea", form));
    }
    const perm = form.closest(".perm");
    if (perm) perm.classList.add("answered");
  });

  // The composer: Enter sends, Shift+Enter is a new line; it grows as you type.
  function autosize(ta) {
    if (!ta) return;
    ta.style.height = "auto";
    ta.style.height = Math.min(ta.scrollHeight, 220) + "px";
  }
  document.addEventListener("input", (e) => {
    if (e.target.matches("textarea")) autosize(e.target);
  });
  document.addEventListener("keydown", (e) => {
    const ta = e.target;
    if (ta.id === "text" && e.key === "Enter" && !e.shiftKey && !e.isComposing) {
      e.preventDefault();
      if (ta.value.trim() || hasFiles()) ta.form.requestSubmit();
    }
    if (e.key === "Escape" && document.body.classList.contains("drawer-open")) closeDrawer();
  });
  document.addEventListener("click", (e) => {
    const ex = e.target.closest("[data-example]");
    if (ex) {
      const ta = $("#text");
      ta.value = ex.textContent.trim();
      autosize(ta);
      ta.focus();
    }
  });

  // Attachments: picked (the camera or the photo library on a phone),
  // dropped on the conversation, or pasted; shown as chips until sent.
  const fileInput = () => $("#files");
  let picked = []; // the input's own list is replaced by each pick
  const hasFiles = () => picked.length > 0;
  function setFiles(list) {
    const input = fileInput();
    if (!input) return;
    const max = +input.dataset.max, many = +input.dataset.many;
    const dt = new DataTransfer();
    for (const f of list) {
      if (f.size > max) { toast(input.dataset.toobig.replace("%s", f.name), "error"); continue; }
      if (dt.files.length >= many) { toast(input.dataset.toomany, "error"); break; }
      dt.items.add(f);
    }
    input.files = dt.files;
    picked = Array.from(dt.files);
    chips();
  }
  function chips() {
    const input = fileInput(), box = $("#composer-files");
    if (!input || !box) return;
    $$("img", box).forEach((img) => URL.revokeObjectURL(img.src));
    box.textContent = "";
    Array.from(input.files).forEach((f, i) => {
      const chip = document.createElement("span");
      chip.className = "file-chip";
      if (f.type.startsWith("image/")) {
        const img = document.createElement("img");
        img.src = URL.createObjectURL(f);
        img.alt = "";
        chip.appendChild(img);
      } else {
        const icon = document.createElement("span");
        icon.className = "att-icon";
        icon.innerHTML = '<svg class="i" viewBox="0 0 24 24" aria-hidden="true"><path d="M14 3H7a2 2 0 00-2 2v14a2 2 0 002 2h10a2 2 0 002-2V8z"/><path d="M14 3v5h5"/></svg>';
        chip.appendChild(icon);
      }
      const name = document.createElement("span");
      name.textContent = f.name;
      chip.appendChild(name);
      const rm = document.createElement("button");
      rm.type = "button";
      rm.setAttribute("aria-label", input.dataset.remove + " " + f.name);
      rm.innerHTML = '<svg class="i" viewBox="0 0 24 24" aria-hidden="true"><path d="M6 6l12 12M18 6L6 18"/></svg>';
      rm.addEventListener("click", () => setFiles(picked.filter((_, j) => j !== i)));
      chip.appendChild(rm);
      box.appendChild(chip);
    });
    box.hidden = !input.files.length;
  }
  document.addEventListener("change", (e) => {
    if (e.target.id === "files") setFiles([...picked, ...Array.from(e.target.files)]);
  });
  document.addEventListener("reset", (e) => {
    if (e.target.id === "composer") setTimeout(() => { picked = []; chips(); });
  });
  document.addEventListener("paste", (e) => {
    if (e.target.id !== "text" || !e.clipboardData || !e.clipboardData.files.length) return;
    e.preventDefault();
    setFiles([...picked, ...Array.from(e.clipboardData.files)]);
  });
  let dragDepth = 0;
  const dropZone = () => $(".chat");
  const dragsFiles = (e) => e.dataTransfer && Array.from(e.dataTransfer.types).includes("Files");
  document.addEventListener("dragenter", (e) => {
    const zone = dropZone();
    if (!zone || !fileInput() || !dragsFiles(e)) return;
    dragDepth++;
    zone.classList.add("dropping");
  });
  document.addEventListener("dragleave", (e) => {
    const zone = dropZone();
    if (!zone || !dragsFiles(e)) return;
    if (--dragDepth <= 0) { dragDepth = 0; zone.classList.remove("dropping"); }
  });
  document.addEventListener("dragover", (e) => {
    if (dropZone() && fileInput() && dragsFiles(e)) e.preventDefault();
  });
  document.addEventListener("drop", (e) => {
    const zone = dropZone();
    if (!zone || !fileInput() || !dragsFiles(e)) return;
    e.preventDefault();
    dragDepth = 0;
    zone.classList.remove("dropping");
    setFiles([...picked, ...Array.from(e.dataTransfer.files)]);
    $("#text").focus();
  });

  // The conversation scrolls to the newest message, unless you scrolled up to read.
  const scroller = () => $("#chat-scroll");
  function nearBottom() {
    const s = scroller();
    return !s || s.scrollHeight - s.scrollTop - s.clientHeight < 160;
  }
  function toBottom(smooth) {
    const s = scroller();
    if (s) s.scrollTo({ top: s.scrollHeight, behavior: smooth ? "smooth" : "auto" });
  }

  // Replace a region's content, keeping what you were typing in it.
  function replaceKeeping(el, html) {
    const typed = {};
    $$("input[id], textarea[id]", el).forEach((i) => { if (i.value) typed[i.id] = i.value; });
    const focused = document.activeElement && el.contains(document.activeElement) ? document.activeElement.id : "";
    el.innerHTML = html;
    Object.entries(typed).forEach(([id, v]) => { const i = document.getElementById(id); if (i) i.value = v; });
    if (focused) { const f = document.getElementById(focused); if (f) f.focus(); }
    htmx.process(el);
  }

  // A card of the conversation (convo.html), in its message's place.
  function placeCard(n) {
    if (!n.id) return;
    const old = document.getElementById(n.id);
    if (old) {
      if (old.outerHTML === n.outerHTML) return;
      if (old.open) n.open = true; // a digest the user unfolded stays open
      if (old.contains(document.activeElement)) return; // not under a finger
      old.replaceWith(n);
    } else {
      const body = $("#m" + n.dataset.msg + " .msg-body");
      if (!body) return;
      let box = $(".pcards", body);
      if (!box) {
        box = document.createElement("div");
        box.className = "pcards";
        body.insertBefore(box, $(":scope > time", body));
      }
      box.appendChild(n);
    }
    htmx.process(n);
  }

  // A card's "Modifier": the composer says what it is about; the user
  // finishes the sentence, and the assistant proposes again.
  document.addEventListener("click", (e) => {
    const b = e.target.closest("[data-card-edit]");
    const ta = $("#text");
    if (!b || !ta) return;
    ta.value = b.dataset.cardEdit;
    autosize(ta);
    ta.focus();
    ta.setSelectionRange(ta.value.length, ta.value.length);
  });

  // The event stream: new messages, the tasks panel, the pending requests.
  function connect() {
    const box = $("#messages");
    if (!box || !window.EventSource) return;
    const es = new EventSource("/events?after=" + (box.dataset.last || 0) + "&tab=" + encodeURIComponent(eripheTab));
    es.addEventListener("notice", (e) => { try { eripheInApp(JSON.parse(e.data)); } catch (err) { /* not for us */ } });
    const conn = $("#conn");
    let failures = 0;
    es.onopen = () => { failures = 0; if (conn) conn.hidden = true; };
    es.onerror = () => {
      failures++;
      if (conn && failures > 1) conn.hidden = false;
      // A stream refused for good (session ended): back to the login page.
      if (es.readyState === EventSource.CLOSED) {
        fetch("/", { redirect: "manual", credentials: "same-origin" }).then((r) => {
          if (r.type === "opaqueredirect") location.href = "/login";
          else setTimeout(connect, 3000);
        }).catch(() => setTimeout(connect, 5000));
      }
    };
    es.addEventListener("message", (e) => {
      const stick = nearBottom();
      const tpl = document.createElement("template");
      tpl.innerHTML = e.data;
      let mine = false;
      const draft = $("#draft");
      Array.from(tpl.content.children).forEach((n) => {
        if (n.id && document.getElementById(n.id)) return;
        if (n.classList.contains("msg-user")) mine = true;
        // The brain's message replaces its draft, in the same frame.
        if (n.classList.contains("msg-brain") && draft && draft.firstElementChild) {
          n.classList.add("settled");
          draft.textContent = "";
        }
        box.appendChild(n);
        htmx.process(n); // an error card's "Try again", a task line's link
      });
      box.dataset.last = e.lastEventId || box.dataset.last;
      const hello = $("#hello");
      if (hello && box.children.length) hello.remove();
      if (stick || mine) toBottom(true);
    });
    // The draft grows in place: only its text changes, so it does not rise again.
    es.addEventListener("draft", (e) => {
      const el = $("#draft");
      if (!el) return;
      const stick = nearBottom();
      const tpl = document.createElement("template");
      tpl.innerHTML = e.data;
      const next = tpl.content.querySelector(".bubble");
      const bubble = el.querySelector(".bubble");
      if (next && bubble) bubble.innerHTML = next.innerHTML;
      else el.replaceChildren(...tpl.content.children);
      const hello = $("#hello");
      if (hello && next) hello.remove();
      if (stick) toBottom(false);
    });
    es.addEventListener("tasks", (e) => {
      const el = $("#tasks");
      if (!el) return;
      el.innerHTML = e.data;
      htmx.process(el);
      badge();
      refreshDrawer();
    });
    // A card changed (decided here, on Telegram or in words), or was
    // attached to a message already shown: put it in place, by its id.
    es.addEventListener("cards", (e) => {
      const stick = nearBottom();
      const tpl = document.createElement("template");
      tpl.innerHTML = e.data;
      Array.from(tpl.content.children).forEach((n) => placeCard(n));
      if (stick) toBottom(true);
    });
    es.addEventListener("pending", (e) => {
      const el = $("#pending");
      if (!el) return;
      const stick = nearBottom();
      replaceKeeping(el, e.data);
      if (stick) toBottom(true);
      badge();
    });
  }

  // The Tâches button's badge: active tasks, highlighted when one needs you.
  function badge() {
    const b = $("#tasks-badge");
    if (!b) return;
    const n = $$("#tasks > ul.cards > li").length;
    const urgent = $$("#pending .perm").length > 0;
    b.hidden = n === 0 && !urgent;
    b.textContent = urgent ? "!" : String(n);
    b.classList.toggle("urgent", urgent);
  }

  // The task drawer opens over the workspace; its body follows the task.
  function openDrawer() {
    document.body.classList.add("drawer-open");
    const close = $("#drawer [data-close]");
    if (close) close.focus();
  }
  function closeDrawer() {
    document.body.classList.remove("drawer-open");
    const d = $("#drawer");
    if (d) setTimeout(() => { if (!document.body.classList.contains("drawer-open")) d.innerHTML = ""; }, 250);
  }
  function refreshDrawer() {
    const body = $("#drawer-body");
    if (!body || !document.body.classList.contains("drawer-open")) return;
    const active = document.activeElement;
    if (active && body.contains(active) && (active.value || "").length) return; // you are typing
    htmx.trigger(body, "refresh");
  }
  document.addEventListener("htmx:afterSwap", (e) => {
    if (e.detail.target.id === "drawer") openDrawer();
  });
  document.addEventListener("click", (e) => {
    if (e.target.closest("[data-close]") || e.target.id === "drawer") closeDrawer();
  });

  // Copy buttons.
  document.addEventListener("click", (e) => {
    const btn = e.target.closest("[data-copy]");
    if (!btn) return;
    const src = document.getElementById(btn.dataset.copy);
    if (!src || !navigator.clipboard) return;
    navigator.clipboard.writeText(src.textContent.trim()).then(() => {
      const label = btn.textContent;
      btn.textContent = document.body.dataset.lang === "en" ? "Copied" : "Copié";
      setTimeout(() => (btn.textContent = label), 1500);
    });
  });

  // Tâches, in the header. On a phone the tasks are a sheet over the
  // conversation: it slides in, and closes with its button, Escape or the
  // browser's back. On a wide screen they are the side panel, which the
  // button hides or shows again (this browser remembers it). From another
  // page, the button comes back to the conversation on its tasks.
  const phone = () => matchMedia("(max-width: 860px)").matches;
  const sideKey = "eriphe.tasks.hidden";
  let sheetEntry = false; // opening the sheet added a step to the history
  function tasksState() {
    const ws = $(".workspace"), btn = $("#tasks-btn");
    if (!ws || !btn) return;
    const open = phone() ? ws.dataset.view === "tasks" : !ws.classList.contains("side-off");
    btn.setAttribute("aria-expanded", String(open));
  }
  function openSheet(push) {
    const ws = $(".workspace");
    ws.dataset.view = "tasks";
    if (push) {
      history.pushState(null, "", "#tasks-panel");
      sheetEntry = true;
    }
    tasksState();
    $("#tasks-panel").focus({ preventScroll: true });
  }
  function sheetClosed() {
    $(".workspace").dataset.view = "chat";
    tasksState();
    $("#tasks-btn").focus({ preventScroll: true });
  }
  function closeSheet() {
    if (sheetEntry) return history.back(); // popstate closes it
    if (location.hash === "#tasks-panel") history.replaceState(null, "", location.pathname + location.search);
    sheetClosed();
  }
  function showSide(show) {
    $(".workspace").classList.toggle("side-off", !show);
    try { show ? localStorage.removeItem(sideKey) : localStorage.setItem(sideKey, "1"); } catch (_) { /* private mode */ }
    tasksState();
    if (show) $("#tasks-panel").focus({ preventScroll: true });
  }
  document.addEventListener("click", (e) => {
    const ws = $(".workspace");
    if (!ws) return;
    if (e.target.closest("#tasks-btn")) {
      e.preventDefault();
      if (!phone()) return showSide(ws.classList.contains("side-off"));
      return ws.dataset.view === "tasks" ? closeSheet() : openSheet(true);
    }
    // its button, or the shade around it
    if (e.target.closest("[data-tasks-close]") || (e.target === ws && ws.dataset.view === "tasks")) closeSheet();
  });
  // Before the drawer's own Escape: the drawer, over the sheet, closes first.
  document.addEventListener("keydown", (e) => {
    const ws = $(".workspace");
    if (e.key !== "Escape" || !ws || ws.dataset.view !== "tasks" || !phone()) return;
    if (document.body.classList.contains("drawer-open") || $("dialog[open]")) return;
    closeSheet();
  }, true);
  window.addEventListener("popstate", () => {
    const ws = $(".workspace");
    if (!ws || !phone()) return;
    if (location.hash === "#tasks-panel") {
      sheetEntry = true;
      openSheet(false);
    } else if (ws.dataset.view === "tasks") {
      sheetEntry = false;
      sheetClosed();
    }
  });
  if ($(".workspace")) {
    let hidden = false;
    try { hidden = localStorage.getItem(sideKey) === "1"; } catch (_) { /* private mode */ }
    if (location.hash === "#tasks-panel") {
      if (phone()) openSheet(false);
      else showSide(true);
    } else if (hidden) {
      $(".workspace").classList.add("side-off");
    }
    tasksState();
    // A phone turned, a window widened: the button says what it now does.
    matchMedia("(max-width: 860px)").addEventListener("change", () => {
      if (!phone()) $(".workspace").dataset.view = "chat";
      tasksState();
    });
  }

  // An address can point into the conversation: a task's drawer (?task=,
  // from a notification), a permission or a message (#perm-…, #m12).
  function pointed() {
    const task = new URLSearchParams(location.search).get("task");
    if (task && $("#drawer")) {
      htmx.ajax("GET", "/tasks/" + encodeURIComponent(task), { target: "#drawer", swap: "innerHTML" });
      history.replaceState(null, "", "/" + location.hash);
    }
    const id = decodeURIComponent(location.hash.slice(1));
    const el = id && /^(perm-|m\d)/.test(id) && document.getElementById(id);
    if (el) {
      // The conversation scrolls to it on its own, never the page.
      const s = scroller();
      if (s && s.contains(el)) {
        const r = el.getBoundingClientRect(), b = s.getBoundingClientRect();
        s.scrollTop += r.top - b.top - Math.max(0, (b.height - r.height) / 2);
      }
      el.classList.add("pointed");
      setTimeout(() => el.classList.remove("pointed"), 2400);
    }
  }

  document.addEventListener("DOMContentLoaded", () => {
    toBottom(false);
    autosize($("#text"));
    badge();
    connect();
    pointed();
    if ("serviceWorker" in navigator && window.isSecureContext) {
      window.eripheWorker = navigator.serviceWorker.register("/sw.js").catch(() => null);
      // A notification clicked while this window is open, and not yet
      // controlled by the worker: it asks the page to go there.
      navigator.serviceWorker.addEventListener("message", (e) => {
        if (e.data && e.data.type === "open" && typeof e.data.url === "string" && new URL(e.data.url).origin === location.origin) location.href = e.data.url;
      });
    }
  });
})();

// A card's "⋯" menu closes when you click elsewhere or press Escape.
document.addEventListener("click", (e) => {
  document.querySelectorAll("details.kebab[open]").forEach((d) => {
    if (!d.contains(e.target)) d.open = false;
  });
});
document.addEventListener("keydown", (e) => {
  if (e.key === "Escape") document.querySelectorAll("details.kebab[open]").forEach((d) => { d.open = false; });
});

// A button with data-dialog opens that <dialog> as a modal; its form
// method="dialog" closes it, and so does a click on the backdrop.
document.addEventListener("click", (e) => {
  const opener = e.target.closest("[data-dialog]");
  if (opener) {
    const d = document.getElementById(opener.dataset.dialog);
    // A link opens the dialog in place of its page, when the page has it.
    if (d && typeof d.showModal === "function") { e.preventDefault(); openDialog(d); }
    return;
  }
  if (e.target instanceof HTMLDialogElement && e.target.classList.contains("modal")) e.target.close();
});

// A link with data-focus scrolls to its target and puts the cursor in that
// field, e.g. "Connect a device" to the name of the new device.
document.addEventListener("click", (e) => {
  const a = e.target.closest("a[data-focus]");
  if (!a) return;
  const field = document.getElementById(a.dataset.focus);
  if (field) setTimeout(() => field.focus({ preventScroll: true }), 300);
});

function openDialog(d) {
  if (!d.open) d.showModal();
  const field = d.dataset.focusOnOpen && document.getElementById(d.dataset.focusOnOpen);
  if (field) field.focus();
}

// A result swapped into a dialog that is closed (re-registering a device
// from its menu fills the connect dialog) opens it; #connect in the address
// (from the welcome page) opens the connect dialog too.
document.body.addEventListener("htmx:afterSwap", (e) => {
  const d = e.target.closest && e.target.closest("dialog.modal");
  if (d && !d.open) d.showModal();
});
// A dialog marked data-reset-on-close starts fresh when it is opened again:
// closed in any way, its forms are reset and its data-clear-on-close parts
// emptied, which also ends the connect dialog's waiting for the device.
document.addEventListener("close", (e) => {
  const d = e.target;
  if (!(d instanceof HTMLDialogElement) || !d.hasAttribute("data-reset-on-close")) return;
  d.querySelectorAll("form").forEach((f) => f.reset());
  d.querySelectorAll("[data-clear-on-close]").forEach((el) => { el.textContent = ""; });
}, true);
if (location.hash === "#connect") {
  const d = document.getElementById("connect-dlg");
  if (d) openDialog(d);
}
// reveal brings el into view by scrolling its nearest scrolling box, and
// only that one (the dialog's step, the page's main): the browser's own
// scrolling into view could move the whole shell. As little as needed, or
// to its top with start.
function reveal(el, start) {
  let s = el.parentElement;
  while (s && s !== document.body) {
    const o = getComputedStyle(s).overflowY;
    if ((o === "auto" || o === "scroll") && s.scrollHeight > s.clientHeight) break;
    s = s.parentElement;
  }
  if (!s || s === document.body) return;
  const r = el.getBoundingClientRect(), b = s.getBoundingClientRect();
  let dy = 0;
  if (start || r.height > b.height - 24) dy = r.top - b.top - 12;
  else if (r.bottom > b.bottom - 12) dy = r.bottom - b.bottom + 12;
  else if (r.top < b.top + 12) dy = r.top - b.top - 12;
  if (dy) s.scrollBy({ top: dy, behavior: "smooth" });
}

// A connection test ("Tester la connexion", .test-btn): the button says
// it is testing, for a moment at least even when the answer is at once,
// its box empties meanwhile, and the result, which comes right under the
// button, is then scrolled into view, in a dialog as on a long page.
const testMin = 600;
document.body.addEventListener("htmx:beforeRequest", (e) => {
  const b = e.detail.elt;
  if (!b || !b.matches || !b.matches(".test-btn")) return;
  if (b.classList.contains("is-busy")) return e.preventDefault(); // one test at a time
  b.classList.add("is-busy");
  b.setAttribute("aria-disabled", "true");
  b.dataset.t0 = Date.now();
  const box = e.detail.target;
  if (box) { box.textContent = ""; box.classList.add("is-waiting"); }
});
document.body.addEventListener("htmx:afterRequest", (e) => {
  const b = e.detail.elt;
  if (!b || !b.matches || !b.matches(".test-btn") || !b.classList.contains("is-busy")) return;
  const box = e.detail.target;
  setTimeout(() => {
    b.classList.remove("is-busy");
    b.removeAttribute("aria-disabled");
    if (!box) return;
    box.classList.remove("is-waiting");
    if (box.firstElementChild) reveal(box);
  }, Math.max(50, testMin - (Date.now() - Number(b.dataset.t0 || 0))));
});

// "Add a tool" in two steps (tools.html, web_tools_dialog.go). A tile, a
// kind of account or any link marked data-step loads its step into
// #tool-step, in place of the search; "Modifier" (data-step-open) opens
// the dialog on the tool's form, scrolled to its data-scroll part if any.
// The address says which step (#add, #add/mail, #edit/mail), the
// browser's Back goes to the step before and, from the first one, closes
// the dialog; closed, the address is as before, so a reload does not open
// it again. Once a tool is saved ("tool-saved"), the dialog closes on the
// list, where the tool's row is lit (a new one at the top) and follows it
// until it is up, and a toast says it. Without this script, every step is
// its own page.
// lit brings a tool's row into view and lights it for a moment.
function lit(name) {
  const row = document.getElementById("tool-" + name);
  if (!row) return;
  row.classList.remove("pointed");
  void row.offsetWidth; // the animation again, when it is lit twice
  row.classList.add("pointed");
  reveal(row);
  setTimeout(() => row.classList.remove("pointed"), 2600);
}

(function () {
  const d = document.getElementById("add-dlg");
  if (!d || typeof d.showModal !== "function") return;
  const pick = d.querySelector("[data-step-pick]");
  const step = document.getElementById("tool-step");
  let shown = ""; // the step shown, as in the address
  let closing = false; // the dialog closed: the history goes back to the page
  let loading = false;
  let follow = null; // the tool just saved, to show in the list once closed
  const depth = () => (history.state && history.state.addStep) || 0;
  const withDialog = (u) => u + (u.includes("?") ? "&" : "?") + "dialog=1";
  const plain = (e) => e.button === 0 && !e.metaKey && !e.ctrlKey && !e.shiftKey && !e.altKey;

  // The address of a step reached without its history (a reload, the way
  // back from an OAuth service).
  function urlOf(hash) {
    const [kind, id] = hash.split("/");
    if (!id) return null;
    if (kind === "edit") return "/settings/tools/" + encodeURIComponent(id) + "/edit";
    const tile = kind === "add" && pick.querySelector('a[data-hash="add/' + CSS.escape(id) + '"]');
    return tile ? tile.dataset.step : null;
  }

  function showPick() {
    step.hidden = true;
    step.textContent = "";
    pick.hidden = false;
    d.classList.remove("on-step");
    d.setAttribute("aria-labelledby", "add-dlg-title");
    shown = "add";
    if (!d.open) d.showModal();
    const q = document.getElementById("finder-q");
    if (q) q.focus();
  }

  // shows the step at url; mode says what the history gets: a new entry
  // (push), this one replaced (replace), or nothing (it is already there).
  function go(url, hash, mode) {
    loading = true;
    return htmx.ajax("GET", withDialog(url), { target: step, swap: "innerHTML" }).then(() => {
      loading = false;
      if (!step.firstElementChild) return;
      pick.hidden = true;
      step.hidden = false;
      d.classList.add("on-step");
      d.setAttribute("aria-labelledby", "step-title");
      if (!d.open) d.showModal();
      hash = hash || step.firstElementChild.dataset.hash; // the link's, else the step's own
      const st = { addStep: depth() + (mode === "push" ? 1 : 0), url };
      if (mode === "push") history.pushState(st, "", "#" + hash);
      else if (mode === "replace") history.replaceState(st, "", "#" + hash);
      shown = hash;
      settle();
    }, () => { loading = false; });
  }

  // after a step came in: its first field, a service to go to
  function settle() {
    step.scrollTop = 0;
    const field = step.querySelector(".step-body input:not([type=hidden]):not([disabled]), .step-body .provider");
    const fine = window.matchMedia("(pointer: fine)").matches; // no keyboard popping up on a phone
    if (field && fine) field.focus({ preventScroll: true });
    else { const t = document.getElementById("step-title"); if (t) { t.tabIndex = -1; t.focus({ preventScroll: true }); } }
    const away = step.querySelector("a[data-go]");
    if (away) setTimeout(() => { if (d.open && step.contains(away)) location.assign(away.href); }, 1200);
  }

  // A form of the step answered with another step (saved, on the way to
  // a service): the address follows it.
  document.body.addEventListener("htmx:afterSwap", (e) => {
    if (e.target !== step || loading) return;
    const h = step.firstElementChild && step.firstElementChild.dataset.hash;
    if (h && h !== shown) {
      history.replaceState({ addStep: depth(), url: urlOf(h) }, "", "#" + h);
      shown = h;
    }
    settle();
  });

  // The list with a tool's row lit, following the tool (?tool=).
  function showTool(t) {
    const u = "/settings/tools?tool=" + encodeURIComponent(t.name) + (t.new ? "&new=1" : "");
    // lit once htmx has settled the rows, which puts their classes back
    return htmx.ajax("GET", u, { target: "#tools-body", select: "#tools-body", swap: "outerHTML" })
      .then(() => setTimeout(() => lit(t.name), htmx.config.defaultSettleDelay + 30));
  }

  function route() {
    if (closing) {
      // back at the page; when it was opened on a step (a link to
      // #add/…), the address drops it, so the next event keeps it closed
      closing = false;
      if (/^#(add|edit|added)(\/|$)/.test(location.hash)) history.replaceState(null, "", location.pathname + location.search);
      return;
    }
    const hash = decodeURIComponent(location.hash.slice(1));
    // the older way back after a save (#added/<name>): the list, its row lit
    const added = /^added\/(.+)$/.exec(hash);
    if (added) {
      history.replaceState(null, "", location.pathname + location.search);
      if (d.open) { follow = { name: added[1] }; return d.close(); }
      return showTool({ name: added[1] });
    }
    if (!/^(add|edit)(\/|$)/.test(hash)) {
      shown = "";
      if (d.open) d.close();
      return;
    }
    if (hash === shown && d.open) return; // popstate and hashchange both come
    shown = hash;
    if (hash === "add") return showPick();
    const url = (history.state && history.state.url) || urlOf(hash);
    if (!url) {
      history.replaceState({ addStep: depth() }, "", "#add");
      return showPick();
    }
    go(url, hash, "none");
  }
  window.addEventListener("popstate", route);
  window.addEventListener("hashchange", route);
  // once the search is set up (setupFinder, below), so its field takes the focus
  document.addEventListener("DOMContentLoaded", () => setTimeout(route));

  document.addEventListener("click", (e) => {
    if (!plain(e)) return;
    const open = e.target.closest('[data-dialog="add-dlg"]');
    if (open) { // the global handler opens it; here, its address
      history.pushState({ addStep: depth() + 1 }, "", "#add");
      return showPick();
    }
    const edit = e.target.closest("a[data-step-open]");
    if (edit) {
      e.preventDefault();
      const part = edit.dataset.scroll;
      return go(edit.getAttribute("href").split("#")[0], edit.dataset.hash, "push").then(() => {
        const el = part && document.getElementById(part);
        if (el && step.contains(el)) reveal(el, true);
      });
    }
    if (!d.contains(e.target)) return;
    const back = e.target.closest("a[data-step-back]");
    if (back) {
      e.preventDefault();
      if (depth() >= 2) return history.back();
      if (back.dataset.hash === "add") {
        history.replaceState({ addStep: depth() }, "", "#add");
        return showPick();
      }
      return go(back.getAttribute("href"), back.dataset.hash, "replace");
    }
    if (e.target.closest("a[data-step-close]")) { e.preventDefault(); return d.close(); }
    const a = e.target.closest("a[data-step]");
    if (a) {
      e.preventDefault();
      go(a.dataset.step || a.getAttribute("href"), a.dataset.hash || shown, "push");
    }
  });

  d.addEventListener("close", () => {
    step.textContent = "";
    step.hidden = true;
    pick.hidden = false;
    d.classList.remove("on-step");
    d.setAttribute("aria-labelledby", "add-dlg-title");
    shown = "";
    const n = depth();
    if (n > 0) { closing = true; history.go(-n); }
    else if (location.hash) history.replaceState(null, "", location.pathname + location.search);
    if (follow) {
      const t = follow;
      follow = null;
      showTool(t);
    }
  });

  // A save in the dialog (web_tools_dialog.go, toolSaved): close on the list.
  document.body.addEventListener("tool-saved", (e) => {
    const t = e.detail || {};
    if (!t.name) return;
    if (t.message) document.dispatchEvent(new CustomEvent("toast", { detail: { message: t.message, kind: "ok" } }));
    follow = t;
    if (d.open) d.close();
    else { follow = null; showTool(t); }
  });

  // Back from a save on a page of its own or from an OAuth service
  // (?tool=): the row is lit, the flash becomes a toast, and the address
  // drops what it said, so a reload shows the list as it is.
  document.addEventListener("DOMContentLoaded", () => {
    const q = new URLSearchParams(location.search);
    const name = q.get("tool");
    if (!name) return;
    const flash = document.querySelector(".ok-note[data-toast]");
    if (flash) {
      document.dispatchEvent(new CustomEvent("toast", { detail: { message: flash.textContent.trim(), kind: "ok" } }));
      flash.remove();
    }
    history.replaceState(history.state, "", location.pathname + location.hash);
    lit(name);
  });
})();

// The workspace on a phone, installed or not: the keyboard, whether the
// user is looking (for the notifications), installing the app and the
// notifications of this device (Settings → App & notifications).
(function () {
  "use strict";

  const $ = (sel, root) => (root || document).querySelector(sel);
  const csrf = () => ($('meta[name="csrf-token"]') || {}).content || "";
  const standalone = () => matchMedia("(display-mode: standalone)").matches || navigator.standalone === true;
  const isIOS = () => /iPhone|iPad|iPod/.test(navigator.userAgent) || (navigator.platform === "MacIntel" && navigator.maxTouchPoints > 1);

  // Installed (home screen): app.css sizes the shell on the large viewport
  // (early.js sets it before the first paint; this is the safety net).
  if (standalone()) document.documentElement.classList.add("app");

  // The settings' sidebar (a wide screen) switches between their pages in
  // place: the browser's Back leaves the settings instead of walking back
  // through every page seen. The list of a phone (/settings) adds its step.
  document.addEventListener("click", (e) => {
    const a = e.target.closest(".settings:not(.is-index) .nav-group > a[href]");
    if (!a || e.defaultPrevented || e.button !== 0 || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return;
    e.preventDefault();
    location.replace(a.href);
  });

  // The keyboard. Chrome on Android shrinks the page for it (the viewport's
  // interactive-widget); Safari on iOS lays it over the page instead: the
  // page follows the visible part, so the composer stays above the
  // keyboard.
  const vv = window.visualViewport;
  if (vv && document.documentElement.classList.contains("shell")) {
    const root = document.documentElement;
    let raf = 0;
    const fit = () => {
      raf = 0;
      const kb = window.innerHeight - vv.height > 120;
      root.classList.toggle("kb", kb);
      if (kb) {
        root.style.setProperty("--app-h", vv.height + "px");
        if (window.scrollY) window.scrollTo(0, 0);
        // Writing in the composer: the newest messages stay in sight.
        const s = document.getElementById("chat-scroll");
        if (s && document.activeElement && document.activeElement.id === "text") s.scrollTop = s.scrollHeight;
      } else {
        root.style.removeProperty("--app-h");
      }
    };
    const later = () => { if (!raf) raf = requestAnimationFrame(fit); };
    vv.addEventListener("resize", later);
    vv.addEventListener("scroll", later);
  }

  // Whether the user is looking: a tab tells eriphe-core when it comes in
  // front or leaves, and every 20 s while it is in front, with its
  // browser's push subscription, so that eriphe-core knows on which device
  // the user is (route.go). The answer may carry a notification routed to
  // this tab, on a page without the event stream.
  if (document.body && document.body.classList.contains("shell") && csrf()) {
    const tab = eripheTab;
    let last = null, at = 0, sub = "";
    if (window.isSecureContext && "serviceWorker" in navigator) {
      navigator.serviceWorker.getRegistration().then((reg) => reg && reg.pushManager && reg.pushManager.getSubscription())
        .then((s) => s ? pushID(s.endpoint) : "").then((id) => { sub = id; if (front()) tell(true); }).catch(() => {});
    }
    const front = () => document.visibilityState === "visible" && document.hasFocus();
    const tell = (force) => {
      const f = front();
      if (!force && f === last && (!f || Date.now() - at < 20000)) return;
      last = f;
      at = Date.now();
      const body = new URLSearchParams({ tab: tab, front: f ? "1" : "0", sub: sub, csrf: csrf() });
      fetch("/presence", { method: "POST", body: body, credentials: "same-origin", keepalive: true,
        headers: { "X-CSRF-Token": csrf() } })
        .then((r) => r.status === 200 ? r.json() : null)
        .then((j) => { if (j && j.notes) eripheInApp(j.notes); })
        .catch(() => {});
    };
    ["focus", "blur", "pageshow"].forEach((ev) => window.addEventListener(ev, () => tell(false)));
    document.addEventListener("visibilitychange", () => tell(false));
    window.addEventListener("pagehide", () => {
      if (navigator.sendBeacon) navigator.sendBeacon("/presence", new URLSearchParams({ tab: tab, front: "0", csrf: csrf() }));
    });
    setInterval(() => { if (front()) tell(true); }, 20000);
    tell(true);
  }

  // Installing the app: Chrome and Edge offer their own prompt, the others
  // are told how.
  let installEvent = null;
  window.addEventListener("beforeinstallprompt", (e) => { installEvent = e; showInstall(); });
  window.addEventListener("appinstalled", () => { installEvent = null; showInstall(); });
  function showInstall() {
    const box = $("[data-install]");
    if (!box) return;
    const done = standalone();
    $("[data-installed]", box).hidden = !done;
    $("[data-install-prompt]", box).hidden = done || !installEvent;
    $("[data-install-lead]", box).hidden = done;
    const here = isIOS() ? "ios" : /Android/.test(navigator.userAgent) ? "android" : "desktop";
    box.querySelectorAll("[data-platform]").forEach((li) => {
      li.hidden = done || li.dataset.platform !== here;
      li.classList.toggle("current", li.dataset.platform === here);
    });
  }
  document.addEventListener("click", (e) => {
    if (!e.target.closest("[data-install-button]") || !installEvent) return;
    installEvent.prompt();
    installEvent.userChoice.finally(() => { installEvent = null; showInstall(); });
  });

  // The notifications of this device.
  const b64 = (s) => {
    const raw = atob(s.replace(/-/g, "+").replace(/_/g, "/") + "===".slice((s.length + 3) % 4));
    return Uint8Array.from(raw, (c) => c.charCodeAt(0));
  };
  // A subscription's id is its endpoint's hash, as eriphe-core computes it (pushID).
  async function pushID(endpoint) {
    const h = new Uint8Array(await crypto.subtle.digest("SHA-256", new TextEncoder().encode(endpoint)));
    return Array.from(h.slice(0, 16), (b) => b.toString(16).padStart(2, "0")).join("");
  }
  const sameKey = (sub, key) => {
    const k = sub.options && sub.options.applicationServerKey;
    if (!k) return true;
    const a = new Uint8Array(k), b = b64(key);
    return a.length === b.length && a.every((v, i) => v === b[i]);
  };

  async function setupPush() {
    const box = $("#push");
    if (!box) return;
    const toggle = $("#push-toggle"), state = $("#push-state"), test = $("#push-test");
    const say = (msg) => { state.textContent = msg || ""; };
    const key = box.dataset.pushKey;
    if (!key) return say(box.dataset.msgOff);
    if (!window.isSecureContext) return say(box.dataset.msgInsecure);
    if (isIOS() && !standalone()) return say(box.dataset.msgIos);
    if (!("serviceWorker" in navigator) || !("PushManager" in window) || !("Notification" in window)) return say(box.dataset.msgUnsupported);
    const reg = await (window.eripheWorker || navigator.serviceWorker.register("/sw.js"));
    if (!reg) return say(box.dataset.msgUnsupported);
    let sub = await reg.pushManager.getSubscription();
    let id = "";

    // The switch is on when this browser's subscription is in the list.
    async function mark() {
      id = sub ? await pushID(sub.endpoint) : "";
      let listed = false;
      document.querySelectorAll("#push-devices [data-push-id]").forEach((li) => {
        const mine = li.dataset.pushId === id;
        listed = listed || mine;
        $("[data-this-device]", li).hidden = !mine;
      });
      toggle.checked = !!sub && listed;
      test.hidden = !toggle.checked;
      $("#push-test-id").value = toggle.checked ? id : "";
      if (Notification.permission === "denied") say(box.dataset.msgDenied);
      else say("");
    }
    async function devices(r) {
      if (!r.ok) throw new Error(await r.text());
      const list = $("#push-devices");
      list.innerHTML = await r.text();
      htmx.process(list);
    }
    const post = (path, body, type) => fetch(path, { method: "POST", body: body, credentials: "same-origin",
      headers: { "X-CSRF-Token": csrf(), "Content-Type": type } });

    toggle.addEventListener("change", async () => {
      toggle.disabled = true;
      try {
        if (toggle.checked) {
          const perm = await Notification.requestPermission();
          if (perm !== "granted") {
            toggle.checked = false;
            say(perm === "denied" ? box.dataset.msgDenied : "");
            return;
          }
          sub = await reg.pushManager.getSubscription();
          if (sub && !sameKey(sub, key)) { await sub.unsubscribe(); sub = null; }
          if (!sub) sub = await reg.pushManager.subscribe({ userVisibleOnly: true, applicationServerKey: b64(key) });
          const body = Object.assign(sub.toJSON(), { app: standalone() });
          await devices(await post("/push/subscribe", JSON.stringify(body), "application/json"));
        } else {
          const gone = id;
          if (sub) await sub.unsubscribe();
          sub = null;
          if (gone) await devices(await post("/push/unsubscribe", new URLSearchParams({ id: gone }), "application/x-www-form-urlencoded"));
        }
      } catch (err) {
        say(box.dataset.msgFailed);
      } finally {
        await mark().catch(() => {});
        toggle.disabled = false;
      }
    });
    // The list changed (a device removed from it): this one may be gone.
    document.body.addEventListener("htmx:afterSwap", (e) => { if (e.target.id === "push-devices") mark(); });
    await mark();
    toggle.disabled = false;
  }

  // "Add a tool" (tools.html, tool-finder): the tiles whose words hold every
  // word typed, accents aside, in the category of the chip pressed. While
  // nothing is searched or picked, the popular tiles come first; a category
  // without a tile left is hidden, and with none at all the address of the
  // service is offered. The arrows move between the tiles shown.
  // Without this script, the field stays hidden and every tile is shown.
  function setupFinder(box) {
    const $ = (sel, root) => (root || document).querySelector(sel);
    const $$ = (sel, root) => Array.from((root || document).querySelectorAll(sel));
    const input = $("input[type=search]", box);
    const chips = $$("[data-chip]", box);
    const popular = $("[data-popular]", box);
    const groups = $$("[data-group]", box);
    const none = $("[data-finder-none]", box);
    const paste = $("[data-finder-paste]", box);
    const fold = (s) => s.toLowerCase().normalize("NFD").replace(/[̀-ͯ]/g, "");
    const tiles = groups.flatMap((g) => $$("[data-search]", g).map((li) => ({ li, words: fold(li.dataset.search) })));
    let cat = "";
    const apply = () => {
      const q = input.value.trim();
      const want = fold(q).split(/\s+/).filter(Boolean);
      tiles.forEach((t) => { t.li.hidden = !want.every((w) => t.words.includes(w)); });
      let shown = 0;
      groups.forEach((g) => {
        const n = (cat === "" || g.dataset.group === cat) ? $$("[data-search]", g).filter((li) => !li.hidden).length : 0;
        g.hidden = n === 0;
        shown += n;
      });
      popular.hidden = want.length > 0 || cat !== "";
      none.hidden = shown > 0;
      // what looks like an address goes along to "Autre service"
      paste.href = "/settings/tools/add/other" + (/^\S+\.\S+$/.test(q) ? "?url=" + encodeURIComponent(q) : "");
    };
    const pick = (c) => {
      cat = c;
      chips.forEach((b) => b.setAttribute("aria-pressed", String(b.dataset.chip === c)));
      apply();
    };
    chips.forEach((b) => b.addEventListener("click", () => pick(b.dataset.chip)));
    input.addEventListener("input", apply);
    const visible = () => $$(".ftile", box).filter((a) => a.offsetParent !== null);
    input.addEventListener("keydown", (e) => {
      if (e.key === "Escape" && input.value && !box.closest("dialog")) { input.value = ""; apply(); }
      if (e.key === "ArrowDown") { const v = visible(); if (v.length) { e.preventDefault(); v[0].focus(); } }
      if (e.key === "Enter") { e.preventDefault(); const v = visible(); if (input.value.trim() && v.length) v[0].click(); }
    });
    box.addEventListener("keydown", (e) => {
      const a = e.target.closest && e.target.closest(".ftile");
      if (!a || !["ArrowDown", "ArrowUp", "ArrowLeft", "ArrowRight"].includes(e.key)) return;
      const v = visible();
      const i = v.indexOf(a);
      const r = a.getBoundingClientRect();
      // up and down: the tile of the next row nearest under or above
      const row = (dir) => v.filter((b) => dir * (b.getBoundingClientRect().top - r.top) > 4)
        .sort((x, y) => dir * (x.getBoundingClientRect().top - y.getBoundingClientRect().top) ||
          Math.abs(x.getBoundingClientRect().left - r.left) - Math.abs(y.getBoundingClientRect().left - r.left))[0];
      const next = { ArrowRight: v[i + 1], ArrowLeft: v[i - 1], ArrowDown: row(1), ArrowUp: row(-1) || input }[e.key];
      if (next) { e.preventDefault(); next.focus(); }
    });
    const dlg = box.closest("dialog");
    if (dlg) dlg.addEventListener("close", () => { input.value = ""; pick(""); });
    $("[data-finder-bar]", box).hidden = false;
    apply();
  }

  document.addEventListener("DOMContentLoaded", () => {
    document.querySelectorAll("[data-finder]").forEach(setupFinder);
    showInstall();
    setupPush().catch(() => {});
  });
})();

// Settings → Appli et notifications → Détails de l'écran: what this device
// reports about its screen, to understand a display issue.
(() => {
  const out = document.getElementById("screen-details");
  if (!out) return;
  const probe = (v) => {
    const d = document.createElement("div");
    d.style.cssText = "position:fixed;top:0;left:0;width:0;visibility:hidden;height:" + v;
    document.body.appendChild(d);
    const h = d.getBoundingClientRect().height;
    d.remove();
    return Math.round(h);
  };
  const show = () => {
    const vv = window.visualViewport;
    out.textContent = [
      "standalone: " + (matchMedia("(display-mode: standalone)").matches || navigator.standalone === true),
      "screen: " + screen.width + " x " + screen.height,
      "inner: " + innerWidth + " x " + innerHeight,
      "visualViewport: " + (vv ? Math.round(vv.width) + " x " + Math.round(vv.height) : "-"),
      "html: " + document.documentElement.clientHeight + ", body: " + Math.round(document.body.getBoundingClientRect().height),
      "100vh: " + probe("100vh") + ", 100dvh: " + probe("100dvh") + ", 100lvh: " + probe("100lvh") + ", 100svh: " + probe("100svh"),
      "safe-area top/bottom: " + probe("env(safe-area-inset-top)") + " / " + probe("env(safe-area-inset-bottom)"),
      "devicePixelRatio: " + devicePixelRatio,
      navigator.userAgent,
    ].join("\n");
  };
  show();
  addEventListener("resize", show);
})();

// Settings → Skills (skills.html): a folder's files keep their paths (a
// form sends a file's name only), and only the input picked last is sent.
document.addEventListener("change", (e) => {
  const input = e.target;
  const form = input.closest && input.closest("[data-skill-upload]");
  if (!form || input.type !== "file") return;
  const folder = form.querySelector("[data-skill-folder]");
  const zip = form.querySelector("[data-skill-zip]");
  (input === folder ? zip : folder).value = "";
  const files = Array.from(input.files || []);
  form.querySelector("[name=paths]").value = JSON.stringify(input === folder ? files.map((f) => f.webkitRelativePath || f.name) : []);
  const picked = form.querySelector("[data-skill-picked]");
  picked.textContent = files.length ? picked.dataset.picked.replace("%d", files.length) : "";
  form.querySelector("[data-skill-send]").disabled = files.length === 0;
  const result = document.getElementById("skill-result");
  if (result) result.textContent = "";
});

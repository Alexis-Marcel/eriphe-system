// Eriphe's workspace: the little the server cannot do. htmx sends the
// forms and actions; this file adds the CSRF header, follows the event
// stream, and handles the composer, the task drawer, toasts and copying.
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
    toast((xhr.responseText || "Erreur").trim().slice(0, 200), "error");
  });
  document.addEventListener("htmx:sendError", () => toast(document.body.dataset.lang === "en" ? "Network error" : "Erreur réseau", "error"));

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

  // The event stream: new messages, the tasks panel, the pending requests.
  function connect() {
    const box = $("#messages");
    if (!box || !window.EventSource) return;
    const es = new EventSource("/events?after=" + (box.dataset.last || 0));
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
    es.addEventListener("pending", (e) => {
      const el = $("#pending");
      if (!el) return;
      const stick = nearBottom();
      replaceKeeping(el, e.data);
      if (stick) toBottom(true);
      badge();
    });
  }

  // The tab bar's badge: active tasks, highlighted when one needs you.
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

  // On a phone the workspace shows one view at a time.
  document.addEventListener("click", (e) => {
    const tab = e.target.closest(".tabbar [data-view]");
    const ws = $(".workspace");
    if (!tab || !ws) return;
    e.preventDefault();
    ws.dataset.view = tab.dataset.view;
    $$(".tabbar [data-view]").forEach((a) => a.toggleAttribute("aria-current", a === tab));
    if (tab.dataset.view === "chat") toBottom(false);
  });

  // On a phone the settings tabs are a row of pills: the current one is
  // brought to the middle of that row, and nothing else scrolls.
  function centerTab() {
    const nav = $(".settings-nav"), tab = $(".settings-nav a[aria-current]");
    if (!nav || !tab || nav.scrollWidth <= nav.clientWidth) return;
    const n = nav.getBoundingClientRect(), t = tab.getBoundingClientRect();
    nav.scrollLeft += t.left + t.width / 2 - (n.left + n.width / 2);
  }

  document.addEventListener("DOMContentLoaded", () => {
    toBottom(false);
    centerTab();
    autosize($("#text"));
    badge();
    connect();
    if ("serviceWorker" in navigator && window.isSecureContext) {
      navigator.serviceWorker.register("/sw.js").catch(() => {});
    }
  });
})();

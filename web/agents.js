"use strict";
// Experimental "New agent" modal: start claude in a folder and watch it in a terminal on
// the dashboard. Uses h/$/view/dur/now from app.js and TRKTerm from term.js.
// Only the terminal on screen holds a websocket: the browser allows 6 connections per host.

window.TRKAgents = (function () {
  const dlg = $("agents"), tabs = $("ag-tabs"), main = $("ag-main");
  let terms = [], selected = "new", viewer = null, popped = new Set();

  const running = () => terms.filter((t) => !t.exited).length;
  const find = (id) => terms.find((t) => t.id === id);

  // header button + power-button wording follow every snapshot
  function update(v) {
    const on = !!(v.experimental_terminals && v.terminals_supported);
    terms = v.terminals || [];
    const btn = $("agent-open"), n = running();
    btn.hidden = !on && !terms.length;
    btn.replaceChildren(...["+ Agent", n ? h("span", { class: "ag-count" }, n) : null].filter(Boolean));
    btn.title = n ? `${n} agent${n === 1 ? "" : "s"} running in TRK` : "Start an agent in a terminal (experimental)";
    if (dlg.open) {
      drawTabs();
      if (selected !== "new" && !find(selected)) show("new"); // stopped elsewhere
      else if (selected !== "new") drawActions();
    }
  }

  function drawTabs() {
    const tab = (id, label, sub, cls) => h("button", { class: "ag-tab " + cls, type: "button", "data-tab": id, "aria-pressed": String(selected === id) },
      h("span", { class: "one" }, label), sub ? h("span", { class: "ag-sub one" }, sub) : null);
    tabs.replaceChildren(
      tab("new", "+ New agent", view && view.experimental_terminals ? "" : "off in ⚙", "ag-new"),
      ...terms.map((t) => tab(t.id, t.name, (t.exited ? "exited · " : "● ") + dur(now() - t.started), t.exited ? "ag-exited" : "ag-live")));
  }

  function drawActions() {
    const t = find(selected);
    $("ag-pop").hidden = !t || t.exited;
    $("ag-stop").hidden = !t;
    $("ag-stop").textContent = t && t.exited ? "Remove" : "Stop claude";
    $("agents-title").textContent = t ? t.name : "+ New agent";
  }

  function dropViewer() { if (viewer) { viewer.dispose(); viewer = null; } }

  function show(id) {
    dropViewer();
    selected = id;
    drawTabs();
    drawActions();
    const t = find(id);
    if (!t) return main.replaceChildren(form());
    if (popped.has(id)) {
      return main.replaceChildren(h("div", { class: "ag-note" }, h("b", null, "Open in its own window"),
        h("button", { class: "btn btn-secondary", type: "button", "data-here": id }, "Show it here instead")));
    }
    const el = h("div", { class: "ag-term" });
    main.replaceChildren(h("div", { class: "ag-meta one", title: t.dir + (t.prompt ? " · " + t.prompt : "") }, t.dir, t.prompt ? " · “" + t.prompt + "”" : ""), el);
    viewer = TRKTerm.attach(el, id, { onExit: () => update(view || {}) });
    setTimeout(() => viewer && viewer.focus(), 50);
  }

  function form() {
    const seen = new Set(), dirs = [];
    for (const s of [...((view && view.sessions) || [])].sort((a, b) => b.last_event_at - a.last_event_at)) {
      if (s.cwd && !seen.has(s.cwd)) { seen.add(s.cwd); dirs.push(s.cwd); }
    }
    const enabled = view && view.experimental_terminals;
    const err = h("span", { class: "ag-err", role: "alert" });
    const dir = h("input", { id: "ag-dir", list: "ag-dirs", autocomplete: "off", spellcheck: "false", value: dirs[0] || "", placeholder: "/path/to/project" });
    const prompt = h("textarea", { id: "ag-prompt", placeholder: "optional, e.g. run the tests and fix what fails" });
    const go = h("button", { class: "btn btn-primary", type: "submit", disabled: enabled ? null : "" }, "Launch claude");
    const f = h("form", { class: "ag-form" },
      h("label", { class: "ag-lab", for: "ag-dir" }, "Folder"), dir,
      h("datalist", { id: "ag-dirs" }, dirs.map((d) => h("option", { value: d }))),
      dirs.length ? h("div", { class: "ag-recent" }, dirs.slice(0, 8).map((d) =>
        h("button", { type: "button", "data-dir": d, title: d }, d.split("/").filter(Boolean).pop() || d))) : null,
      h("label", { class: "ag-lab", for: "ag-prompt" }, "First prompt"), prompt,
      h("div", { class: "ag-go" }, go, err),
      h("p", { class: "meta" }, enabled
        ? "Runs claude in a terminal owned by TRK. Closing this window keeps it running; it stops with Stop, or when TRK restarts or updates."
        : "Experimental terminals are off. Turn them on in ⚙ Card layout → Experimental."));
    f.addEventListener("click", (e) => { const b = e.target.closest("[data-dir]"); if (b) dir.value = b.dataset.dir; });
    f.addEventListener("submit", async (e) => {
      e.preventDefault();
      err.textContent = "";
      go.disabled = true;
      try {
        const r = await fetch("/v1/terms", { method: "POST", headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ dir: dir.value.trim(), prompt: prompt.value }) });
        if (!r.ok) throw new Error((await r.text()).trim());
        const t = await r.json();
        terms = [...terms, t];
        show(t.id);
      } catch (x) { err.textContent = String(x.message || x); go.disabled = false; }
    });
    return f;
  }

  tabs.addEventListener("click", (e) => { const b = e.target.closest("[data-tab]"); if (b) show(b.dataset.tab); });
  main.addEventListener("click", (e) => { const b = e.target.closest("[data-here]"); if (b) { popped.delete(b.dataset.here); show(b.dataset.here); } });
  $("ag-pop").addEventListener("click", () => { const t = find(selected); if (!t) return; if (TRKTerm.popOut(t)) { popped.add(t.id); show(t.id); } });
  $("ag-stop").addEventListener("click", async () => {
    const t = find(selected);
    if (!t || (!t.exited && !confirm(`Stop claude in ${t.name}?\n\nThe agent is killed; its conversation can be resumed later with claude --resume.`))) return;
    dropViewer();
    await TRKTerm.stop(t.id);
    terms = terms.filter((x) => x.id !== t.id);
    show("new");
  });
  $("ag-close").addEventListener("click", () => dlg.close());
  dlg.addEventListener("close", dropViewer); // closing never stops an agent
  $("agent-open").addEventListener("click", () => {
    const live = terms.filter((t) => !t.exited);
    dlg.showModal();
    show(live.length ? live[live.length - 1].id : "new");
  });

  return { update, running };
})();

"use strict";
// Layout drawer: choose card widgets, their order and options, footer stats and
// dashboard areas. Uses layout/setLayout/presetLayout from app.js. Changes apply live.

(function () {
  const dlg = $("layout"), body = $("layout-body");
  const NOTES = {
    task: "Declared task, 2 lines", step: "step i/N · what it's doing", progress: "20-cell gradient bar",
    reality: "Shown when intent and actions disagree", tools: "Latest tool calls", ctx: "ctx % with the 40/60 bands",
    model: "Model the session runs", files: "Files read / edited, last 30 min",
  };
  let dragFrom = null;

  const clone = () => JSON.parse(JSON.stringify(layout));
  const update = (fn) => { const next = clone(); fn(next); next.preset = matchPreset(next); setLayout(next); draw(); };
  const matchPreset = (l) => Object.keys(PRESETS).find((name) => {
    const p = presetLayout(name);
    return JSON.stringify([p.slots, p.tools, p.hints, p.reserve, p.footer]) === JSON.stringify([l.slots, l.tools, l.hints, l.reserve, l.footer]);
  }) || "";

  function move(from, to) {
    if (from == null || to < 0 || to >= layout.slots.length || from === to) return;
    update((l) => { const [x] = l.slots.splice(from, 1); l.slots.splice(to, 0, x); });
  }

  function slotRow(slot, i) {
    const opts = [];
    if (slot.on && slot.id === "tools") {
      opts.push(h("label", null, "lines ",
        h("select", { id: "opt-tools" }, [1, 2, 3, 4, 5].map((n) => h("option", { value: n, selected: n === layout.tools ? "" : null }, n)))));
    }
    if (slot.on && slot.id === "ctx") opts.push(h("label", null, h("input", { id: "opt-hints", type: "checkbox", checked: layout.hints ? "" : null }), " show hints at 50% / 70%"));
    if (slot.on && slot.id === "reality") opts.push(h("label", null, h("input", { id: "opt-reserve", type: "checkbox", checked: layout.reserve ? "" : null }), " keep its space when empty"));

    const li = h("li", { class: "ed-slot" + (slot.on ? "" : " off"), draggable: "true" },
      h("span", { class: "ed-grip", "aria-hidden": "true" }, "⠿"),
      h("div", { class: "ed-main" },
        h("label", { class: "ed-name" }, h("input", { id: "on-" + slot.id, type: "checkbox", checked: slot.on ? "" : null }), " " + WIDGETS[slot.id]),
        h("span", { class: "ed-note" }, NOTES[slot.id]),
        opts.length ? h("div", { class: "ed-opts" }, opts) : null),
      h("div", { class: "ed-move" },
        h("button", { type: "button", "aria-label": "Move " + WIDGETS[slot.id] + " up", disabled: i === 0 ? "" : null, "data-move": i + ":" + (i - 1) }, "▲"),
        h("button", { type: "button", "aria-label": "Move " + WIDGETS[slot.id] + " down", disabled: i === layout.slots.length - 1 ? "" : null, "data-move": i + ":" + (i + 1) }, "▼")));
    li.addEventListener("dragstart", () => { dragFrom = i; li.classList.add("dragging"); });
    li.addEventListener("dragend", () => li.classList.remove("dragging"));
    li.addEventListener("dragover", (e) => { e.preventDefault(); li.classList.add("over"); });
    li.addEventListener("dragleave", () => li.classList.remove("over"));
    li.addEventListener("drop", (e) => { e.preventDefault(); li.classList.remove("over"); move(dragFrom, i); });
    return li;
  }

  const toggle = (id, label, on) => h("button", { class: "ed-tog", type: "button", "aria-pressed": on ? "true" : "false", "data-tog": id }, label);

  function draw() {
    const shown = layout.slots.filter((s) => s.on).length;
    body.replaceChildren(
      h("section", { class: "ed-group" }, h("h3", null, "Presets"),
        h("div", { class: "ed-row" }, Object.keys(PRESETS).map((name) =>
          h("button", { class: "btn btn-secondary", type: "button", "aria-pressed": layout.preset === name ? "true" : "false", "data-preset": name }, name)))),
      h("section", { class: "ed-group" }, h("h3", null, "Widgets, top to bottom ", h("span", { class: "meta" }, `${shown} of ${layout.slots.length} shown`)),
        h("ul", { class: "ed-slots" }, layout.slots.map(slotRow)),
        h("p", { class: "meta" }, "Drag ⠿ or use the arrows to reorder. The card header and footer stay pinned.")),
      h("section", { class: "ed-group" }, h("h3", null, "Footer stats ", h("span", { class: "meta" }, `${layout.footer.length}/4`)),
        h("div", { class: "ed-row" }, Object.entries(FOOTER_STATS).map(([k, label]) => toggle("foot:" + k, label, layout.footer.includes(k))))),
      h("section", { class: "ed-group" }, h("h3", null, "Dashboard areas"),
        h("div", { class: "ed-row" }, Object.entries(AREAS).map(([k, label]) => toggle("area:" + k, label, layout.areas[k])))));
  }

  body.addEventListener("click", (e) => {
    const t = e.target.closest("button");
    if (!t) return;
    if (t.dataset.preset) { const p = presetLayout(t.dataset.preset); p.areas = layout.areas; setLayout(p); draw(); }
    else if (t.dataset.move) { const [a, b] = t.dataset.move.split(":").map(Number); move(a, b); }
    else if (t.dataset.tog) {
      const [kind, k] = t.dataset.tog.split(":");
      update((l) => {
        if (kind === "area") l.areas[k] = !l.areas[k];
        else if (l.footer.includes(k)) l.footer = l.footer.filter((x) => x !== k);
        else if (l.footer.length < 4) l.footer.push(k);
      });
    }
  });
  body.addEventListener("change", (e) => {
    const t = e.target;
    if (t.id.startsWith("on-")) update((l) => { l.slots.find((s) => s.id === t.id.slice(3)).on = t.checked; });
    else if (t.id === "opt-tools") update((l) => { l.tools = Number(t.value); });
    else if (t.id === "opt-hints") update((l) => { l.hints = t.checked; });
    else if (t.id === "opt-reserve") update((l) => { l.reserve = t.checked; });
  });

  $("layout-open").addEventListener("click", () => { draw(); dlg.showModal(); });
  $("layout-close").addEventListener("click", () => dlg.close());
  $("layout-reset").addEventListener("click", () => { setLayout(presetLayout("Default")); draw(); });
  dlg.addEventListener("click", (e) => { if (e.target === dlg) dlg.close(); }); // click outside closes
})();

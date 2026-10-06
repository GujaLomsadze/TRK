"use strict";
// TRK dashboard. Builds DOM with createElement/textContent only: agent text is untrusted.

let view = null;
let skew = 0; // server clock - local clock
const DASH = "—";
const $ = (id) => document.getElementById(id);
const now = () => Date.now() + skew;

function h(tag, props, ...kids) {
  const el = document.createElement(tag);
  for (const [k, v] of Object.entries(props || {})) {
    if (v == null || v === false) continue;
    if (k === "class") el.className = v;
    else el.setAttribute(k, v);
  }
  for (const kid of kids.flat(Infinity)) {
    if (kid == null || kid === false) continue;
    el.append(kid instanceof Node ? kid : document.createTextNode(String(kid)));
  }
  return el;
}

// Per-cell ASCII bars: 20 cells, each coloured by its own position (picked design "B").
const CELLS = 20;
const RGB = { red: [255, 90, 74], amber: [255, 178, 122], green: [92, 255, 157], yellow: [255, 214, 107], hot: [255, 74, 61], deep: [255, 40, 60] };
const mix = (a, b, t) => a.map((v, k) => Math.round(v + (b[k] - v) * Math.min(1, Math.max(0, t))));
// progress: red → amber → green, so a bar only shows green near the finish
const progCell = (i) => (i < 12 ? mix(RGB.red, RGB.amber, i / 11) : mix(RGB.amber, RGB.green, (i - 11) / 8));
// context (user bands): green < 40, yellow 40–59, red ≥ 60; cell i covers up to (i+1)*5 %
const ctxCell = (i) => {
  const p = (i + 1) * 5;
  if (p < 40) return mix(RGB.green, RGB.yellow, Math.max(0, (p - 20) / 20) * 0.5);
  if (p < 60) return mix(RGB.yellow, RGB.amber, (p - 40) / 20);
  return mix(RGB.hot, RGB.deep, (p - 60) / 40);
};
// plan usage (5h / 7d): green < 50, yellow 50–79, red ≥ 80
const useCell = (i) => {
  const p = (i + 1) * 5;
  if (p < 50) return mix(RGB.green, RGB.yellow, Math.max(0, (p - 25) / 25) * 0.5);
  if (p < 80) return mix(RGB.yellow, RGB.amber, (p - 50) / 30);
  return mix(RGB.hot, RGB.deep, (p - 80) / 20);
};
function cellBar(frac, colorOf, cls = "") {
  const n = Math.round(Math.max(0, Math.min(1, frac || 0)) * CELLS);
  const el = h("span", { class: "cellbar " + cls }, "[");
  for (let i = 0; i < CELLS; i++) {
    if (i < n) {
      const c = colorOf(i).join(",");
      el.append(h("span", { style: `color:rgb(${c});text-shadow:0 0 6px rgba(${c},.55)` }, "█"));
    } else el.append(h("span", { class: "off" }, "░"));
  }
  el.append("]");
  return el;
}
function dur(ms) {
  const s = Math.max(0, Math.floor(ms / 1000));
  if (s < 60) return s + "s";
  const m = Math.floor(s / 60);
  if (m < 60) return m + "m";
  const hr = Math.floor(m / 60);
  if (hr < 48) return hr + "h" + String(m % 60).padStart(2, "0") + "m";
  return Math.floor(hr / 24) + "d";
}
function tokens(n) {
  if (!n) return "0";
  if (n >= 1e6) return (n / 1e6).toFixed(1) + "M";
  if (n >= 1e3) return Math.round(n / 1e3) + "K";
  return String(n);
}
const pct = (v) => (v == null ? DASH : Math.round(v) + "%");
const money = (v) => "$" + (v || 0).toFixed(2);

const CHIP = {
  working: ["WORKING", "work"], waiting: ["WAITING ON YOU", "warn"], blocked: ["BLOCKED", "red"],
  looping: ["LOOPING?", "warn"], idle: ["IDLE", "done"], done: ["DONE", "ok"],
};
const NEED_LABEL = { permission: "permission", question: "question", blocked: "blocked" };

function renderHeader(v) {
  $("host").textContent = location.host;
  const st = v.stats;
  $("stats").replaceChildren(
    h("div", { class: "stat" }, h("b", null, st.agents), h("span", null, "agents")),
    h("div", { class: "stat" }, h("b", null, st.working), h("span", null, "working")),
    h("div", { class: "stat" + (st.needs_you ? " hot" : "") }, h("b", null, st.needs_you), h("span", null, "needs you")),
    h("div", { class: "stat" }, h("b", null, tokens(st.tokens_today)), h("span", null, "tokens today")),
  );
  const a = v.account || {};
  $("limits").replaceChildren(limit("5h", a.five_h_pct, a.five_h_reset), limit("7d", a.seven_d_pct, a.seven_d_reset));
}

function limit(label, p, reset) {
  const live = p != null && (!reset || reset > now());
  return h("div", { class: "limit" },
    h("span", { class: "limit-label" }, label),
    cellBar(live ? p / 100 : 0, useCell),
    h("span", { class: "limit-pct", style: live ? `color:var(${p >= 80 ? "--red" : p >= 50 ? "--yellow" : "--ok"})` : null }, live ? Math.round(p) + "%" : DASH),
    h("span", { class: "meta" }, live && reset ? "resets " + dur(reset - now()) : ""));
}

function ctxGauge(s) {
  const c = s.ctx || { level: "none" };
  const size = s.ctx_size ? " of " + tokens(s.ctx_size) : "";
  return h("div", { class: "ctx ctx-" + c.level },
    h("div", { class: "ctx-line" }, "ctx " + pct(s.ctx_pct) + size),
    cellBar((s.ctx_pct || 0) / 100, ctxCell, "small"),
    layout.hints ? h("div", { class: "ctx-hints" }, (c.hints || []).map((t) => h("div", { class: "ctx-hint one", title: t }, "▲ " + t))) : null);
}

// ---- card layout (edited in the Layout drawer, saved in this browser) ----
const LAYOUT_KEY = "trk.layout.v1";
const WIDGETS = {
  task: "Task", step: "Step", progress: "Progress bar", reality: "Reality check",
  tools: "Recent tools", ctx: "Context gauge", model: "Model", files: "Files touched",
  prompt: "Last prompt", reply: "Last reply", lines: "Lines changed", busy: "Busy vs waiting", subagents: "Subagents",
};
const FOOTER_STATS = { elapsed: "Elapsed", tokens: "Tokens", cost: "Cost", model: "Model", last: "Last activity", calls: "Tool calls" };
const AREAS = { needs: "Needs you", collisions: "Collisions", timeline: "Timeline (bottom)", limits: "Plan usage (header)" };
const PRESETS = {
  Default: { order: ["task", "progress", "step", "reality", "prompt", "reply", "lines", "busy", "subagents", "ctx", "tools", "model", "files"], on: ["task", "progress", "step", "reality", "prompt", "reply", "lines", "busy", "subagents", "ctx"], tools: 3, hints: true, reserve: false, footer: ["elapsed", "cost", "model", "last"] },
  Compact: { on: ["task", "progress", "ctx"], tools: 1, hints: false, reserve: false, footer: ["elapsed", "cost"] },
  Detailed: { on: ["task", "step", "progress", "reality", "tools", "ctx", "model", "files", "prompt", "reply", "lines", "busy", "subagents"], tools: 5, hints: true, reserve: true, footer: ["elapsed", "tokens", "cost", "last"] },
  "Context watch": { on: ["ctx", "task", "progress"], tools: 3, hints: true, reserve: false, footer: ["tokens", "cost"] },
};
function presetLayout(name) {
  const p = PRESETS[name];
  const first = p.order || p.on; // a preset may place hidden widgets too
  const order = [...first, ...Object.keys(WIDGETS).filter((id) => !first.includes(id))];
  return { preset: name, slots: order.map((id) => ({ id, on: p.on.includes(id) })), tools: p.tools, hints: p.hints,
    reserve: p.reserve, footer: [...p.footer], areas: { needs: true, collisions: true, timeline: true, limits: true }, grid: { cols: 0, rows: 0 } };
}
// Card grid: columns and rows on screen; 0 means automatic.
const GRID_COLS = 6, GRID_ROWS = 4;
const gridNum = (v, max) => (Number.isInteger(v) && v >= 0 && v <= max ? v : 0);
// loadLayout trusts nothing from storage: unknown widgets are dropped, missing ones appended (off).
function loadLayout() {
  const def = presetLayout("Default");
  let saved = null;
  try { saved = JSON.parse(localStorage.getItem(LAYOUT_KEY) || "null"); } catch { saved = null; }
  if (!saved || !Array.isArray(saved.slots)) return def;
  const seen = new Set();
  const slots = saved.slots.filter((x) => WIDGETS[x.id] && !seen.has(x.id) && seen.add(x.id)).map((x) => ({ id: x.id, on: !!x.on }));
  for (const id of Object.keys(WIDGETS)) if (!seen.has(id)) slots.push({ id, on: false });
  return {
    preset: PRESETS[saved.preset] ? saved.preset : "",
    slots,
    tools: Math.min(5, Math.max(1, Number(saved.tools) || 3)),
    hints: saved.hints !== false,
    reserve: saved.reserve !== false,
    footer: (Array.isArray(saved.footer) ? saved.footer : def.footer).filter((k) => FOOTER_STATS[k]).slice(0, 4),
    areas: Object.fromEntries(Object.keys(AREAS).map((k) => [k, !saved.areas || saved.areas[k] !== false])),
    grid: { cols: gridNum(saved.grid && saved.grid.cols, GRID_COLS), rows: gridNum(saved.grid && saved.grid.rows, GRID_ROWS) },
  };
}
let layout = loadLayout();
function setLayout(next) {
  layout = next;
  try { localStorage.setItem(LAYOUT_KEY, JSON.stringify(layout)); } catch { /* private window: keep in memory */ }
  cards.clear();
  $("grid").replaceChildren();
  applyAreas();
  applyGrid();
  render();
}
function applyAreas() {
  document.body.classList.toggle("no-side", !layout.areas.needs && !layout.areas.collisions);
  document.body.classList.toggle("no-timeline", !layout.areas.timeline);
  document.body.classList.toggle("no-limits", !layout.areas.limits);
  document.querySelector(".side .needs-box").hidden = !layout.areas.needs;
  document.querySelector(".side .coll-box").hidden = !layout.areas.collisions;
}

function applyGrid() {
  const g = $("grid"), { cols, rows } = layout.grid;
  g.classList.toggle("fixed-cols", cols > 0);
  g.style.setProperty("--cols", cols || 1);
  g.classList.toggle("fixed-rows", rows > 0);
  fitRows();
}
// Fixed rows: size cards so `rows` rows fill the window above the docked timeline.
function fitRows() {
  const g = $("grid"), rows = layout.grid.rows;
  if (!rows) { g.style.removeProperty("--row-h"); return; }
  const dock = layout.areas.timeline ? document.querySelector(".timeline").offsetHeight + 28 : 16;
  const top = g.getBoundingClientRect().top + scrollY;
  const avail = innerHeight - top - dock - 16 * (rows - 1);
  g.style.setProperty("--row-h", Math.max(160, Math.floor(avail / rows)) + "px");
}
addEventListener("resize", fitRows);

const lastTop = new Map(); // session_id -> newest tool line seen
const mark = (st) => (st === "ok" ? "✓" : st === "fail" ? "✗" : "…");

// Each widget renders into a slot with a reserved height, so every card has the same shape.
const RENDER = {
  task: (s) => h("p", { class: "slot-task" + (s.task ? "" : " muted"), title: s.task || "" }, s.task || "no task declared"),
  // "step i/N" sits beside the progress bar; it falls back to this line when the bar is hidden
  step: (s) => {
    const n = s.step_n || 0, bar = layout.slots.some((x) => x.id === "progress" && x.on);
    const count = bar ? "" : n ? `step ${s.step_i || 0}/${n}` : "step " + DASH;
    return h("div", { class: "slot-step one", title: s.step_text || "" },
      count, count && s.step_text ? " · " : "", s.step_text || (count ? "" : "\u00a0"),
      s.status === "working" ? h("span", { class: "cursor", "aria-hidden": "true" }) : null);
  },
  progress: (s) => h("div", { class: "slot-bar prog-row" }, cellBar(s.step_n ? (s.step_i || 0) / s.step_n : 0, progCell),
    h("span", { class: "prog-count" }, s.step_n ? `step ${s.step_i || 0}/${s.step_n}` : "step " + DASH)),
  reality: (s) => (s.reality || layout.reserve)
    ? h("div", { class: "slot-reality" }, s.reality ? h("div", { class: "reality" }, h("span", { class: "reality-label" }, "reality"), s.reality) : null)
    : null,
  tools: (s, fresh) => {
    const rows = (s.recent || []).slice(0, layout.tools);
    while (rows.length < layout.tools) rows.push(null);
    return h("ul", { class: "tools" }, rows.map((c, k) => c
      ? h("li", { class: "tool one tool-" + c.state + (k === 0 && fresh ? " flash" : ""), title: c.tool + " " + c.summary },
          "› ", h("span", { class: "tool-name" }, c.tool), " ", c.summary, " ", h("span", { class: "mark" }, mark(c.state)))
      : h("li", { class: "tool one blank" }, k === 0 && !(s.recent || []).length ? "no tool calls yet" : "\u00a0")));
  },
  ctx: (s) => ctxGauge(s),
  model: (s) => h("div", { class: "slot-line one" }, "model · ", h("span", { class: "val" }, s.model || DASH)),
  files: (s) => h("div", { class: "slot-line one" }, "files · ", h("span", { class: "val" }, `${s.files_read || 0} read · ${s.files_edited || 0} edited`), h("span", { class: "muted" }, " (30 min)")),
  // The widgets below always render their box (dimmed placeholder when there is no
  // data yet), so cards keep the same shape and don't jump as data arrives.
  prompt: (s) => s.last_prompt
    ? h("div", { class: "w-prompt" },
        h("div", { class: "w-who" }, "You", h("span", { class: "live-ago", "data-since": s.last_prompt_at }, dur(now() - s.last_prompt_at) + " ago")),
        h("div", { class: "w-txt clamp2", title: s.last_prompt }, s.last_prompt))
    : h("div", { class: "w-prompt ghost" }, h("div", { class: "w-who" }, "You"), h("div", { class: "w-txt clamp2" }, "no prompt yet")),
  // the reply is the result: shown once the agent has stopped
  reply: (s) => {
    const stopped = s.status === "done" || s.status === "idle";
    if (s.last_reply && stopped) {
      return h("div", { class: "w-reply" }, h("div", { class: "w-who" }, "Claude"), h("div", { class: "w-txt clamp2", title: s.last_reply }, s.last_reply));
    }
    return h("div", { class: "w-reply ghost" }, h("div", { class: "w-who" }, "Claude"),
      h("div", { class: "w-txt clamp2" }, s.status === "working" || s.status === "looping" ? "working…" : stopped ? "no reply yet" : "waiting on you"));
  },
  lines: (s) => {
    const a = s.lines_added || 0, d = s.lines_removed || 0;
    const na = a + d ? Math.max(a ? 1 : 0, Math.round((a / (a + d)) * CELLS)) : 0;
    const nd = a + d ? Math.min(CELLS - na, Math.max(d ? 1 : 0, Math.round((d / (a + d)) * CELLS))) : 0;
    return h("div", { class: "w-lines" + (a || d ? "" : " ghost") },
      h("span", { class: "w-lines-n" }, h("span", { class: "w-add" }, "+" + tokens(a)), " ", h("span", { class: "w-del" }, "−" + tokens(d))),
      h("span", { class: "w-cells", "aria-hidden": "true" }, "[", h("span", { class: "w-add" }, "█".repeat(na)), h("span", { class: "w-del" }, "█".repeat(nd)),
        h("span", { class: "off" }, "░".repeat(CELLS - na - nd)), "]"));
  },
  busy: (s) => {
    const b = s.wall_ms ? Math.round(Math.min(1, (s.api_ms || 0) / s.wall_ms) * 100) : 0;
    return h("div", { class: "w-busy" + (s.wall_ms ? "" : " ghost"), title: s.wall_ms ? `model working ${dur(s.api_ms || 0)} of ${dur(s.wall_ms)}` : "" },
      h("div", { class: "w-split" }, h("div", { class: "w-model", style: `width:${b}%` }), h("div", { class: "w-rest", style: `width:${100 - b}%` })),
      h("div", { class: "w-legend" }, h("span", null, h("i", { class: "w-model" }), "model " + (s.wall_ms ? b + "%" : DASH)), h("span", null, h("i", { class: "w-rest" }), "tools / you " + (s.wall_ms ? 100 - b + "%" : DASH))));
  },
  subagents: (s) => {
    const types = Object.entries(s.subagents || {}).sort((x, y) => y[1] - x[1]);
    const n = types.reduce((sum, [, c]) => sum + c, 0), bg = s.bg_running || 0;
    if (!n && !bg) return h("div", { class: "slot-line one ghost" }, "▸ no subagents");
    const full = [n ? `${n} subagent${n === 1 ? "" : "s"}: ` + types.map(([t, c]) => t + " ×" + c).join(", ") : "", bg ? bg + " in background" : ""].filter(Boolean).join(" · ");
    return h("div", { class: "slot-line one", title: full }, "▸ ",
      n ? [h("span", { class: "val" }, n + (n === 1 ? " subagent" : " subagents")), " · ", types.map(([t, c]) => t + (c > 1 ? " ×" + c : "")).join(", ")] : null,
      bg ? [n ? " · " : "", h("span", { class: "w-bg" }, bg + " in background")] : null);
  },
};
const FOOT = {
  elapsed: (s) => h("span", { class: "live-elapsed", "data-since": s.started_at }, "elapsed " + dur(now() - s.started_at)),
  tokens: (s) => h("span", null, tokens((s.tokens_in || 0) + (s.tokens_out || 0)) + " tok"),
  cost: (s) => h("span", null, money(s.cost_usd)),
  model: (s) => h("span", null, s.model || DASH),
  last: (s) => h("span", { class: "live-last", "data-since": s.last_event_at }, "last " + dur(now() - s.last_event_at)),
  calls: (s) => h("span", null, (s.tool_calls || 0) + " calls"),
};

function card(s) {
  const [label, tone] = CHIP[s.status] || CHIP.working;
  const where = [s.repo, s.branch].filter(Boolean).join(" · ") || s.cwd || "";
  // flash the newest tool line when it changes
  const top = s.recent && s.recent[0] ? s.recent[0].tool + s.recent[0].summary : "";
  const fresh = top && lastTop.has(s.session_id) && lastTop.get(s.session_id) !== top;
  lastTop.set(s.session_id, top);
  return h("article", { class: "panel card st-" + s.status + (s.attention ? " attention" : ""), "data-open": s.session_id, tabindex: "0",
    "aria-label": s.name + ", " + label.toLowerCase() + ". Press Enter to open its agent drawer" },
    // ✕ sits left of the name so the status chip keeps the same spot on every card
    h("div", { class: "card-head" },
      s.status === "idle" || s.status === "done"
        ? h("button", { class: "dismiss", type: "button", "data-dismiss": s.session_id, "aria-label": "Hide " + s.name, title: "Hide this card (it comes back if the session gets busy again)" }, "✕")
        : null,
      h("div", { class: "head-text" }, h("h3", { class: "card-name one", title: s.name }, s.name),
        h("div", { class: "meta one", title: where }, s.in_trk ? h("span", { class: "in-trk", title: "Running in a TRK terminal" }, "⌨ ") : null, where || DASH)),
      // WORKING: light runs through the letters (the text needs its own box for background-clip)
      h("span", { class: "chip chip-" + tone }, tone === "work" ? h("span", { class: "shine" }, label) : label)),
    layout.slots.filter((x) => x.on).map((x) => RENDER[x.id](s, fresh)),
    layout.footer.length ? h("footer", { class: "card-foot" }, layout.footer.map((k) => FOOT[k](s))) : null);
}

// Keyed update: a card's DOM is replaced only when its data changed, and cards
// keep their slot. Rebuilding everything every second made cards flicker.
const cards = new Map(); // session_id -> { sig, el }

function renderGrid(v) {
  const grid = $("grid");
  if (!v.sessions.length) {
    cards.clear();
    grid.replaceChildren(h("div", { class: "panel empty" }, "No agents yet. Start Claude Code (after `trk init`) or run `trk start \"task\"`."));
    return;
  }
  grid.querySelector(".empty")?.remove();
  const seen = new Set();
  // sessions in a live TRK terminal: linked by pid (running) or started with --resume (terminals)
  const inTRK = new Set(Object.entries(v.running || {}).filter(([, t]) => t).map(([sid]) => sid));
  for (const t of v.terminals || []) if (!t.exited && t.session_id) inTRK.add(t.session_id);
  v.sessions.forEach((s, i) => {
    seen.add(s.session_id);
    if (inTRK.has(s.session_id)) s = { ...s, in_trk: true };
    const sig = JSON.stringify(s);
    let c = cards.get(s.session_id);
    if (!c || c.sig !== sig) {
      const el = card(s);
      if (c) c.el.replaceWith(el);
      c = { sig, el };
      cards.set(s.session_id, c);
    }
    if (grid.children[i] !== c.el) grid.insertBefore(c.el, grid.children[i] || null);
  });
  for (const [id, c] of cards) if (!seen.has(id)) { c.el.remove(); cards.delete(id); }
}

function tickClocks() {
  for (const el of document.querySelectorAll(".live-elapsed")) el.textContent = "elapsed " + dur(now() - Number(el.dataset.since));
  for (const el of document.querySelectorAll(".live-last")) el.textContent = "last " + dur(now() - Number(el.dataset.since));
  for (const el of document.querySelectorAll(".live-ago")) el.textContent = dur(now() - Number(el.dataset.since)) + " ago";
}

function renderSide(v) {
  $("needs").replaceChildren(...(v.needs.length
    ? v.needs.map((n) => h("li", null,
        h("div", { class: "need-head" }, n.name, " · ", NEED_LABEL[n.type] || n.type, " · ", dur(now() - n.ts)),
        h("div", { class: "need-text" }, n.text || DASH)))
    : [h("li", { class: "muted" }, "nothing pending")]));
  $("collisions").replaceChildren(...(v.collisions.length
    ? v.collisions.map((c) => h("li", null,
        h("div", { class: "need-text" }, c.path),
        h("div", { class: "meta" }, c.names.join(" ↔ "), " · ", dur(now() - c.last_ts), " ago")))
    : [h("li", { class: "muted" }, "none")]));
}

// Docked at the bottom, fits the window (no horizontal scroll). Positions are
// percentages of the 30-minute window; one lane per card, in card order.
function renderTimeline(v) {
  const end = now(), span = 30 * 60 * 1000, start = end - span;
  const x = (t) => ((Math.max(start, Math.min(end, t)) - start) / span) * 100;
  const axis = h("div", { class: "tl-track tl-axis-track" });
  for (let m = 30; m >= 0; m -= 5) axis.append(h("span", { class: "tl-tick", style: `left:${x(end - m * 60000)}%` }, m ? `-${m}m` : "now"));
  const lanes = v.sessions.map((s) => {
    const track = h("div", { class: "tl-track" });
    for (const b of s.timeline || []) {
      const l = x(b.start), r = x(b.end || end);
      track.append(h("span", { class: "tl-block tl-" + b.cat, style: `left:${l}%;width:max(2px,${r - l}%)`,
        title: `${b.cat} · ${dur((b.end || end) - b.start)}` }));
    }
    return h("div", { class: "tl-lane" }, h("div", { class: "tl-label one", title: s.name }, s.name), track);
  });
  $("timeline").replaceChildren(
    h("div", { class: "tl-lane tl-axis" }, h("div", { class: "tl-label" }, ""), axis),
    ...(lanes.length ? lanes : [h("div", { class: "muted" }, "no activity in the last 30 minutes")]));
}

// Keep page content clear of the docked timeline.
new ResizeObserver(([e]) => { document.body.style.setProperty("--dock-h", e.target.offsetHeight + "px"); fitRows(); }).observe(document.querySelector(".timeline"));

function render() {
  if (!view) return;
  renderHeader(view);
  renderGrid(view);
  renderSide(view);
  renderTimeline(view);
  if (window.TRKAgents) TRKAgents.update(view);
}

// Between snapshots only clocks move; header/inbox/timeline are cheap and have no animation.
let ticks = 0;
function tick() {
  if (!view) return;
  tickClocks();
  renderHeader(view);
  renderSide(view);
  if (++ticks % 5 === 0) renderTimeline(view);
}

function setConn(live) {
  const el = $("conn");
  el.textContent = live ? "● live" : stopped ? "■ stopped" : "○ reconnecting";
  el.classList.toggle("live", live);
}

// ✕ on idle/done cards: the daemon hides the session until it shows new activity.
// Anywhere else on a card opens its agent drawer (unless the click ended a text selection).
$("grid").addEventListener("keydown", (e) => {
  if ((e.key === "Enter" || e.key === " ") && e.target.matches("[data-open]") && window.TRKAgents) {
    e.preventDefault();
    TRKAgents.openSession(e.target.dataset.open);
  }
});
$("grid").addEventListener("click", async (e) => {
  const b = e.target.closest("[data-dismiss]");
  if (!b) {
    const c = e.target.closest("[data-open]");
    if (c && window.TRKAgents && !String(getSelection())) TRKAgents.openSession(c.dataset.open);
    return;
  }
  b.disabled = true;
  try { await fetch("/v1/sessions/" + encodeURIComponent(b.dataset.dismiss) + "/dismiss", { method: "POST" }); }
  catch { b.disabled = false; }
});

let stopped = false;
$("power").addEventListener("click", async () => {
  const own = window.TRKAgents ? TRKAgents.running() : 0;
  const warn = own ? `\n\n${own} agent${own === 1 ? "" : "s"} started from the dashboard will be stopped (resume them later from their cards).` : "";
  if (!confirm("Stop TRK?\n\nClaude keeps working; nothing is recorded until you run `trk open`." + warn)) return;
  try { await fetch("/v1/stop", { method: "POST" }); } catch { /* daemon may close the connection as it exits */ }
  stopped = true;
  $("stopped").hidden = false;
  setConn(false);
});

let es = null;
function connect() {
  es = new EventSource("/v1/stream");
  es.addEventListener("snapshot", (e) => {
    view = JSON.parse(e.data);
    skew = view.now - Date.now();
    setConn(true);
    render();
  });
  es.onerror = () => { setConn(false); if (stopped) es.close(); }; // EventSource reconnects by itself
}
// A page parked in the back/forward cache keeps its stream open, and the browser allows only
// 6 connections per host: a few Fleet/Stats trips would leave the next page unable to load.
addEventListener("pagehide", () => { if (es) { es.close(); es = null; } });
addEventListener("pageshow", (e) => { if (e.persisted && !es && !stopped) connect(); });

applyAreas();
applyGrid();
connect();
setInterval(tick, 1000);

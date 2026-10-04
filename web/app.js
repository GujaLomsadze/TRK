"use strict";
// TRK dashboard. Builds DOM with createElement/textContent only: agent text is untrusted.

let view = null;
let skew = 0; // server clock - local clock
let firstTimeline = true;
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

function bar(frac, cells = 20) {
  const f = Math.max(0, Math.min(1, frac || 0));
  const n = Math.round(f * cells);
  return "[" + "█".repeat(n) + "░".repeat(cells - n) + "]";
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
  working: ["WORKING", "accent"], waiting: ["WAITING ON YOU", "warn"], blocked: ["BLOCKED", "warn"],
  looping: ["LOOPING?", "warn"], idle: ["IDLE", "done"], done: ["DONE", "done"],
};
const TONE = { working: "accent", waiting: "warn", blocked: "warn", looping: "warn", idle: "done", done: "done" };
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
    h("span", { class: "ascii " + (live && p >= 80 ? "tone-warn" : "tone-accent") }, bar(live ? p / 100 : 0)),
    h("span", null, live ? Math.round(p) + "%" : DASH),
    h("span", { class: "meta" }, live && reset ? "resets " + dur(reset - now()) : ""));
}

function ctxGauge(s) {
  const c = s.ctx || { level: "none" };
  const size = s.ctx_size ? " of " + tokens(s.ctx_size) : "";
  return h("div", { class: "ctx ctx-" + c.level },
    h("div", { class: "ctx-line" },
      h("span", null, "ctx " + pct(s.ctx_pct) + size),
      h("span", { class: "ascii ctx-bar" }, bar((s.ctx_pct || 0) / 100, 10))),
    (c.hints || []).map((t) => h("div", { class: "ctx-hint" }, "▲ " + t)));
}

function card(s) {
  const [label, tone] = CHIP[s.status] || CHIP.working;
  const n = s.step_n || 0, i = s.step_i || 0;
  const where = [s.repo, s.branch].filter(Boolean).join(" · ") || s.cwd || "";
  const mark = (st) => (st === "ok" ? "✓" : st === "fail" ? "✗" : "…");
  return h("article", { class: "panel card" + (s.attention ? " attention" : "") },
    h("div", { class: "card-head" },
      h("div", null, h("h3", { class: "card-name" }, s.name), h("div", { class: "meta" }, where)),
      h("span", { class: "chip chip-" + tone }, label)),
    s.task ? h("p", { class: "task" }, s.task) : h("p", { class: "task muted" }, "no task declared"),
    n || s.step_text ? h("div", { class: "step" }, n ? `step ${i}/${n}` : "step", s.step_text ? " · " + s.step_text : "") : null,
    n ? h("div", { class: "ascii tone-" + (TONE[s.status] || "accent") }, bar(i / n)) : null,
    s.reality ? h("div", { class: "reality" }, h("span", { class: "reality-label" }, "reality"), s.reality) : null,
    s.recent && s.recent.length
      ? h("ul", { class: "tools" }, s.recent.map((c) =>
          h("li", { class: "tool tool-" + c.state, title: c.tool + " " + c.summary },
            "› ", h("span", { class: "tool-name" }, c.tool), " ", c.summary, " ", h("span", { class: "mark" }, mark(c.state)))))
      : null,
    ctxGauge(s),
    h("footer", { class: "card-foot" },
      h("span", { class: "live-elapsed", "data-since": s.started_at }, "elapsed " + dur(now() - s.started_at)),
      h("span", null, tokens((s.tokens_in || 0) + (s.tokens_out || 0)) + " tok"),
      h("span", null, money(s.cost_usd))));
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
  v.sessions.forEach((s, i) => {
    seen.add(s.session_id);
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

function renderTimeline(v) {
  const W = 1800, end = now(), start = end - 30 * 60 * 1000;
  const x = (t) => ((Math.max(start, Math.min(end, t)) - start) / (end - start)) * W;
  const axis = h("div", { class: "tl-track", style: `width:${W}px` });
  for (let m = 30; m >= 0; m -= 5) axis.append(h("span", { class: "tl-tick", style: `left:${x(end - m * 60000)}px` }, m ? `-${m}m` : "now"));
  const lanes = v.sessions.filter((s) => s.timeline && s.timeline.length).map((s) => {
    const track = h("div", { class: "tl-track", style: `width:${W}px` });
    for (const b of s.timeline) {
      const l = x(b.start), r = x(b.end || end);
      track.append(h("span", { class: "tl-block tl-" + b.cat, style: `left:${l}px;width:${Math.max(2, r - l)}px`,
        title: `${b.cat} · ${dur((b.end || end) - b.start)}` }));
    }
    return h("div", { class: "tl-lane" }, h("div", { class: "tl-label", title: s.name }, s.name), track);
  });
  $("timeline").replaceChildren(
    h("div", { class: "tl-axis" }, h("div", { class: "tl-label" }, ""), axis),
    ...(lanes.length ? lanes : [h("div", { class: "muted" }, "no activity in the last 30 minutes")]));
  if (firstTimeline && lanes.length) {
    $("tl-scroll").scrollLeft = $("tl-scroll").scrollWidth;
    firstTimeline = false;
  }
}

function render() {
  if (!view) return;
  renderHeader(view);
  renderGrid(view);
  renderSide(view);
  renderTimeline(view);
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
  el.textContent = live ? "● live" : "○ reconnecting";
  el.classList.toggle("live", live);
}

function connect() {
  const es = new EventSource("/v1/stream");
  es.addEventListener("snapshot", (e) => {
    view = JSON.parse(e.data);
    skew = view.now - Date.now();
    setConn(true);
    render();
  });
  es.onerror = () => setConn(false); // EventSource reconnects by itself
}

connect();
setInterval(tick, 1000);

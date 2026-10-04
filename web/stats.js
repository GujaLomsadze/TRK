"use strict";
// TRK Stats page: plan-usage history (5h and weekly) with an "at this pace" projection.
// Builds DOM with createElement/textContent only, like app.js.

const MIN = 60e3, HOUR = 60 * MIN, DAY = 24 * HOUR;
const $ = (id) => document.getElementById(id);
let skew = 0; // server clock - local clock
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
const NS = "http://www.w3.org/2000/svg";
function svgEl(tag, attrs, parent, text) {
  const el = document.createElementNS(NS, tag);
  for (const [k, v] of Object.entries(attrs)) el.setAttribute(k, v);
  if (text != null) el.textContent = text;
  if (parent) parent.append(el);
  return el;
}

const hm = (t) => { const d = new Date(t); return String(d.getHours()).padStart(2, "0") + ":" + String(d.getMinutes()).padStart(2, "0"); };
const day = (t) => new Date(t).toLocaleDateString("en", { weekday: "short" });
const dayhm = (t) => day(t) + " " + hm(t);
function dur(ms) {
  const m = Math.max(0, Math.round(ms / MIN));
  if (m >= 1440) return Math.floor(m / 1440) + "d " + Math.floor((m % 1440) / 60) + "h";
  if (m >= 60) return Math.floor(m / 60) + "h " + (m % 60) + "m";
  return m + "m";
}
const tone3 = (p) => (p >= 80 ? "red" : p >= 50 ? "yellow" : "ok");

// One entry per plan window. `bucket` matches the server's grouping in /v1/limits/history.
const CHARTS = {
  c5: { win: "5h", title: "5-hour limit", len: 5 * HOUR, bucket: MIN, recent: 30 * MIN, recentLabel: "30m",
        when: hm, perUnit: HOUR, unit: "%/h", digits: 1, short: "5h", acct: ["five_h_pct", "five_h_reset"] },
  c7: { win: "7d", title: "Weekly limit", len: 7 * DAY, bucket: 30 * MIN, recent: DAY, recentLabel: "24h",
        when: dayhm, perUnit: DAY, unit: "%/day", digits: 0, short: "weekly", acct: ["seven_d_pct", "seven_d_reset"] },
};
const PACE_KEY = "trk.stats.pace.v1";
const pace = (() => {
  const d = { c5: "recent", c7: "window" };
  try { return { ...d, ...JSON.parse(localStorage.getItem(PACE_KEY) || "{}") }; } catch { return d; }
})();

// ---------- data ----------
const data = {}; // id -> { start, reset, pts: [[ts, pct]] }

async function load(id) {
  const c = CHARTS[id];
  try {
    const r = await fetch("/v1/limits/history?window=" + c.win);
    if (!r.ok) throw new Error(r.status);
    const j = await r.json();
    skew = j.now - Date.now();
    data[id] = { start: j.start, reset: j.resets_at, pts: j.points.map((p) => [p.ts, p.pct]) };
  } catch {
    if (!data[id]) data[id] = { start: now() - c.len, reset: 0, pts: [], failed: true };
  }
  render(id);
}

// A new reading from the live stream: merge into its bucket, or refetch when the window rolled over.
function onAccount(a) {
  for (const id in CHARTS) {
    const c = CHARTS[id], d = data[id];
    if (!d) continue;
    const pct = a[c.acct[0]], reset = a[c.acct[1]] || 0;
    if (pct == null || !a.updated_at) continue;
    if (reset && reset !== d.reset) { load(id); continue; }
    const last = d.pts[d.pts.length - 1];
    if (last && a.updated_at <= last[0]) continue;
    if (last && Math.floor(last[0] / c.bucket) === Math.floor(a.updated_at / c.bucket)) d.pts[d.pts.length - 1] = [a.updated_at, Math.max(last[1], pct)];
    else d.pts.push([a.updated_at, pct]);
    render(id);
  }
}

// value at time t: the last reading at or before t (the window starts at 0%)
function valueAt(d, t) {
  let v = null;
  for (const p of d.pts) { if (p[0] > t) break; v = p[1]; }
  return v ?? (t >= d.start ? 0 : null);
}

function project(c, d, basis) {
  const tNow = Math.max(now(), d.pts.length ? d.pts[d.pts.length - 1][0] : 0);
  const used = d.pts.length ? d.pts[d.pts.length - 1][1] : 0;
  const reset = d.reset;
  let rate = used / Math.max(tNow - d.start, 1); // % per ms, whole window
  if (basis === "recent") {
    const from = Math.max(d.start, tNow - c.recent);
    rate = Math.max(0, (used - valueAt(d, from)) / Math.max(tNow - from, 1));
  }
  const atReset = used + rate * (reset - tNow);
  const capAt = used >= 100 ? tNow : rate > 0 ? tNow + (100 - used) / rate : Infinity;
  const budget = Math.max(0, (100 - used) / Math.max(reset - tNow, 1)); // %/ms that lands on 100 at reset
  const lastTs = d.pts.length ? d.pts[d.pts.length - 1][0] : 0;
  return { tNow, used, reset, rate, atReset, capAt, budget, lastTs };
}

// ---------- render ----------
function render(id) {
  const c = CHARTS[id], d = data[id], box = $(id);
  if (!d) return;
  const basis = pace[id];
  const seg = h("div", { class: "seg", role: "group", "aria-label": "pace basis" },
    h("button", { type: "button", "aria-pressed": String(basis === "window"), "data-b": "window" }, "pace: whole window"),
    h("button", { type: "button", "aria-pressed": String(basis === "recent"), "data-b": "recent" }, "pace: last " + c.recentLabel));
  seg.addEventListener("click", (e) => {
    const b = e.target.closest("button");
    if (!b) return;
    pace[id] = b.dataset.b;
    try { localStorage.setItem(PACE_KEY, JSON.stringify(pace)); } catch { /* per-browser convenience only */ }
    render(id);
  });
  const head = h("div", { class: "chart-head" }, h("h2", null, c.title), seg);

  if (!d.reset || !d.pts.length) {
    box.replaceChildren(head, h("div", { class: "empty-note" },
      h("b", null, d.failed ? "Can't reach TRK" : "No readings yet"),
      d.failed ? "The daemon didn't answer. Start it with `trk open`, then reload."
        : "TRK records your " + c.short + " plan usage from Claude Code's statusline. It fills in once a Claude session is running."));
    return;
  }

  const p = project(c, d, basis);
  const hits = p.capAt <= p.reset;
  const rateTxt = (r) => (r * c.perUnit).toFixed(c.digits) + c.unit;
  const tone = hits ? "red" : p.atReset >= 80 ? "yellow" : "ok";
  const B = (cls, txt) => h("b", { class: cls }, txt);

  let word, sentence, delta;
  if (p.used >= 100) {
    word = "▲ Limit hit";
    sentence = ["You're at the limit until the reset at ", B("c-red", c.when(p.reset)), "."];
    delta = [B("c-red", dur(p.reset - p.tNow)), "until you can work again"];
  } else {
    word = hits ? "▲ Slow down" : tone === "yellow" ? "● Hold this pace" : "✓ On track";
    sentence = hits
      ? ["At this pace you run out at ", B("c-red", c.when(p.capAt)), ", ", B("", dur(p.reset - p.capAt)), " before the reset."]
      : tone === "yellow"
      ? ["You'll land at about ", B("c-yellow", Math.round(p.atReset) + "%"), " by the reset. Don't speed up."]
      : ["You'll land at about ", B("c-ok", Math.round(p.atReset) + "%"), " by the reset. Plenty of room."];
    if (p.rate > p.budget) delta = [B("c-red", "−" + Math.round((1 - p.budget / p.rate) * 100) + "%"), "cut your pace by this much to make it"];
    else if (p.rate === 0) delta = [B("c-ok", rateTxt(p.budget)), "idle now; this pace is still safe"];
    else delta = [B("c-ok", "+" + Math.round((p.budget / p.rate - 1) * 100) + "%"), "room to speed up and still make it"];
  }
  const top = Math.max(p.rate, p.budget) * 1.1 || 1;
  const bar = (r, cls) => h("div", { class: "pace-track" }, h("div", { class: "pace-fill " + cls, style: `width:${((r / top) * 100).toFixed(1)}%` }));
  const fast = p.rate > p.budget ? "red" : "ok";
  const advice = h("div", { class: "advice " + { red: "v-red", yellow: "v-warn", ok: "v-ok" }[tone] },
    h("div", { class: "say" }, h("div", { class: "word c-" + tone }, word), h("p", null, sentence)),
    h("div", { class: "pace" },
      h("div", { class: "pace-row" }, h("span", { class: "lab" }, "your pace"), bar(p.rate, "c-" + fast), h("span", { class: "val c-" + fast }, rateTxt(p.rate))),
      h("div", { class: "pace-row" }, h("span", { class: "lab" }, "safe pace"), bar(p.budget, ""), h("span", { class: "val" }, rateTxt(p.budget))),
      h("div", { class: "pace-delta" }, delta)));

  const stale = p.tNow - p.lastTs > 10 * MIN;
  const num = (v, cls) => h("span", { class: "num" + (cls ? " c-" + cls : "") }, v, h("small", null, "%"));
  const tile = (lab, numEl, sub) => h("div", { class: "tile" }, h("span", { class: "lab" }, lab), numEl, h("span", { class: "sub2" }, sub));
  const tiles = h("div", { class: "tiles" },
    tile("used now", num(Math.round(p.used), tone3(p.used)), stale ? "last reading " + dur(p.tNow - p.lastTs) + " ago" : "of this " + c.short + " limit"),
    tile("at reset", num(hits ? 100 : Math.round(p.atReset), tone), hits ? "capped at " + c.when(p.capAt) : "if you keep this pace"),
    tile("resets in", h("span", { class: "num" }, dur(p.reset - p.tNow)), c.when(p.reset)));

  const plot = h("div", { class: "plot" });
  const legend = h("div", { class: "legend" },
    h("span", null, h("i", { class: "k" }), "used"),
    h("span", null, h("i", { class: "k dash" }), "if you keep this pace"),
    h("span", null, h("i", { class: "k dot" }), "max safe pace → 100% at reset"),
    h("span", null, h("i", { class: "k capk" }), "limit"));
  box.replaceChildren(head, advice, tiles, plot, legend);
  draw(plot, id, c, d, p, hits);
}

function ticks(c, d) {
  const out = [];
  if (c.win === "7d") { // local midnights
    const t = new Date(d.start); t.setHours(24, 0, 0, 0);
    for (; t.getTime() <= d.reset; t.setDate(t.getDate() + 1)) out.push({ t: t.getTime(), label: day(t) });
  } else {
    for (let t = Math.ceil(d.start / (30 * MIN)) * 30 * MIN; t <= d.reset; t += 30 * MIN) out.push({ t, label: hm(t) });
  }
  return out;
}

function draw(plot, id, c, d, p, hits) {
  const W = plot.clientWidth, H = plot.clientHeight;
  if (!W || !H) return;
  const m = { l: 46, r: 14, t: 16, b: 28 };
  const x = (t) => m.l + ((t - d.start) / c.len) * (W - m.l - m.r);
  const endV = p.used + p.rate * (p.reset - p.tNow);
  const yMax = Math.max(110, Math.min(endV + 5, 160));
  const y = (v) => m.t + (1 - v / yMax) * (H - m.t - m.b);
  const svg = svgEl("svg", { viewBox: `0 0 ${W} ${H}`, role: "img", "aria-label": `${c.title}: ${Math.round(p.used)}% used` }, plot);
  const gid = "fill-" + id;
  const g = svgEl("linearGradient", { id: gid, x1: 0, y1: 0, x2: 0, y2: 1 }, svgEl("defs", {}, svg));
  svgEl("stop", { offset: 0, "stop-color": "#FF5A4A", "stop-opacity": 0.22 }, g);
  svgEl("stop", { offset: 1, "stop-color": "#FF5A4A", "stop-opacity": 0 }, g);

  // zone bands: same 50/80 thresholds as the header usage colours
  svgEl("rect", { class: "band-y", x: m.l, width: W - m.l - m.r, y: y(80), height: y(50) - y(80) }, svg);
  svgEl("rect", { class: "band-r", x: m.l, width: W - m.l - m.r, y: y(100), height: y(80) - y(100) }, svg);
  for (const v of [0, 25, 50, 75, 100]) {
    svgEl("line", { class: "grid-l", x1: m.l, x2: W - m.r, y1: y(v), y2: y(v) }, svg);
    svgEl("text", { class: "ax", x: m.l - 6, y: y(v) + 4, "text-anchor": "end" }, svg, v + "%");
  }
  svgEl("line", { class: "cap", x1: m.l, x2: W - m.r, y1: y(100), y2: y(100) }, svg);
  let lastX = -1e9;
  for (const tk of ticks(c, d)) {
    const xx = x(tk.t);
    if (xx - lastX < 64 || xx > W - m.r) continue;
    lastX = xx;
    svgEl("text", { class: "ax", x: xx, y: H - 6, "text-anchor": "middle" }, svg, tk.label);
  }

  // used: solid line through readings; a gap with no readings is drawn as a faint dotted bridge
  const pts = d.pts, maxGap = 5 * c.bucket;
  const P = (q) => x(q[0]).toFixed(1) + " " + y(q[1]).toFixed(1);
  const runs = [];
  let run = [];
  pts.forEach((q, i) => {
    if (i && q[0] - pts[i - 1][0] > maxGap) { runs.push(run); run = []; }
    run.push(q);
  });
  runs.push(run);
  const bridges = [[[d.start, 0], pts[0]]];
  for (let i = 1; i < runs.length; i++) bridges.push([runs[i - 1][runs[i - 1].length - 1], runs[i][0]]);
  for (const [a, b] of bridges) if (b[0] - a[0] > c.bucket) svgEl("path", { class: "gap", d: `M${P(a)}L${P(b)}` }, svg);
  for (const r of runs) {
    const line = "M" + r.map(P).join("L");
    svgEl("path", { d: line + `L${x(r[r.length - 1][0])} ${y(0)}L${x(r[0][0])} ${y(0)}Z`, fill: `url(#${gid})` }, svg);
    svgEl("path", { class: "used", d: r.length > 1 ? line : `${line}h1` }, svg);
  }
  // since the last reading, usage is unknown: hold it flat up to now
  if (p.tNow - p.lastTs > c.bucket) svgEl("path", { class: "gap", d: `M${P([p.lastTs, p.used])}L${P([p.tNow, p.used])}` }, svg);

  // max safe pace (now → 100% at reset) and the projection at the current pace
  svgEl("line", { class: "budget", x1: x(p.tNow), y1: y(p.used), x2: x(p.reset), y2: y(100) }, svg);
  svgEl("line", { class: "proj" + (hits ? " hot" : ""), x1: x(p.tNow), y1: y(p.used), x2: x(p.reset), y2: y(Math.min(endV, yMax)) }, svg);

  svgEl("line", { class: "nowl", x1: x(p.tNow), x2: x(p.tNow), y1: m.t, y2: y(0) }, svg);
  svgEl("text", { class: "lbl", x: x(p.tNow) + 6, y: m.t + 10 }, svg, "now");
  svgEl("line", { class: "resetl", x1: x(p.reset), x2: x(p.reset), y1: m.t, y2: y(0) }, svg);
  svgEl("circle", { class: "dot", cx: x(p.tNow), cy: y(p.used), r: 4 }, svg);
  svgEl("text", { class: "lbl", x: x(p.tNow) - 8, y: y(p.used) - 8, "text-anchor": "end" }, svg, Math.round(p.used) + "%");

  if (hits) {
    svgEl("circle", { class: "dot r", cx: x(p.capAt), cy: y(100), r: 4 }, svg);
    svgEl("text", { class: "lbl r", x: x(p.capAt), y: y(100) + 22, "text-anchor": "start", dx: 8 }, svg, "out at " + hm(p.capAt));
  } else {
    svgEl("text", { class: "lbl m", x: x(p.reset) - 6, y: y(endV) + 18, "text-anchor": "end" }, svg, "~" + Math.round(endV) + "% at reset");
  }

  // crosshair + tooltip
  const xh = svgEl("line", { class: "xh", y1: m.t, y2: y(0), visibility: "hidden" }, svg);
  const tip = h("div", { class: "tip" });
  tip.hidden = true;
  plot.append(tip);
  const hit = svgEl("rect", { x: m.l, y: 0, width: W - m.l - m.r, height: H, fill: "transparent" }, svg);
  hit.addEventListener("pointermove", (e) => {
    const px = e.clientX - svg.getBoundingClientRect().left;
    const t = d.start + ((px - m.l) / (W - m.l - m.r)) * c.len;
    let v, kind;
    if (t <= p.tNow) { v = valueAt(d, t); kind = t < pts[0][0] ? "before first reading" : "used"; }
    else { v = p.used + p.rate * (t - p.tNow); kind = "projected"; }
    xh.setAttribute("x1", px); xh.setAttribute("x2", px); xh.setAttribute("visibility", "visible");
    tip.hidden = false;
    tip.style.left = px + "px";
    tip.style.top = y(Math.min(v, yMax)) + "px";
    tip.replaceChildren(h("b", null, Math.min(v, 999).toFixed(0) + "%"), " ", h("span", { class: "m" }, kind + " · " + c.when(t)));
    hovering = id;
  });
  hit.addEventListener("pointerleave", () => { xh.setAttribute("visibility", "hidden"); tip.hidden = true; hovering = null; });
}

// ---------- live ----------
let hovering = null;
function setConn(live) {
  const el = $("conn");
  el.textContent = live ? "● live" : "○ reconnecting";
  el.classList.toggle("live", live);
}
function connect() {
  const es = new EventSource("/v1/stream");
  es.addEventListener("snapshot", (e) => {
    const v = JSON.parse(e.data);
    skew = v.now - Date.now();
    setConn(true);
    if (v.account) onAccount(v.account);
  });
  es.onerror = () => setConn(false); // EventSource reconnects by itself
}

$("host").textContent = location.host;
for (const id in CHARTS) load(id);
connect();
// time moves the "now" line and countdowns; skip a redraw while the pointer is reading a chart
setInterval(() => { for (const id in CHARTS) if (hovering !== id) render(id); }, 30e3);
let rz;
addEventListener("resize", () => { clearTimeout(rz); rz = setTimeout(() => { for (const id in CHARTS) render(id); }, 100); });

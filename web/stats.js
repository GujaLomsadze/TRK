"use strict";
// TRK Stats page: history from /v1/stats (spend, time split, waits, activity, tools, commands,
// files, context, sessions). Builds DOM with createElement/textContent only, like app.js:
// session names, commands and file paths come from agents and are untrusted.

const $ = (id) => document.getElementById(id);
const NS = "http://www.w3.org/2000/svg";
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
function sv(tag, attrs, parent, text) {
  const el = document.createElementNS(NS, tag);
  for (const [k, v] of Object.entries(attrs)) el.setAttribute(k, v);
  if (text != null) el.textContent = text;
  if (parent) parent.append(el);
  return el;
}
const css = (v) => getComputedStyle(document.documentElement).getPropertyValue(v).trim();
const money = (v) => "$" + (v || 0).toFixed(2);
const tok = (n) => (n >= 1e6 ? (n / 1e6).toFixed(1) + "M" : n >= 1e3 ? Math.round(n / 1e3) + "K" : String(Math.round(n || 0)));
function dur(sec) {
  sec = Math.max(0, sec);
  if (sec < 60) return Math.round(sec) + "s";
  if (sec < 3600) return Math.floor(sec / 60) + "m " + String(Math.round(sec % 60)).padStart(2, "0") + "s";
  if (sec < 86400 * 2) return Math.floor(sec / 3600) + "h " + String(Math.round((sec % 3600) / 60)).padStart(2, "0") + "m";
  return Math.round(sec / 86400) + "d";
}
const when = (ms) => new Date(ms).toLocaleString("en-GB", { weekday: "short", day: "numeric", month: "short", hour: "2-digit", minute: "2-digit" });
const none = (text) => h("div", { class: "st-none" }, text);

// ---------- tooltip ----------
const tip = $("tip");
function showTip(e, rows) {
  tip.replaceChildren(...rows);
  tip.hidden = false;
  const r = tip.getBoundingClientRect();
  let x = e.clientX + 14, y = e.clientY + 14;
  if (x + r.width > innerWidth - 8) x = e.clientX - r.width - 14;
  if (y + r.height > innerHeight - 8) y = e.clientY - r.height - 14;
  tip.style.left = x + "px";
  tip.style.top = y + "px";
}
const hideTip = () => { tip.hidden = true; };
function hover(el, rows) { el.addEventListener("mousemove", (e) => showTip(e, rows())); el.addEventListener("mouseleave", hideTip); }
const trow = (sw, label, val) => h("div", { class: "tr" }, h("span", null, sw ? h("i", { class: "st-sw", style: "background:" + sw + ";margin-right:6px" }) : null, label), h("span", null, val));

// ---------- state ----------
const PREF_KEY = "trk.stats.v1";
const pref = (() => { try { return JSON.parse(localStorage.getItem(PREF_KEY) || "{}"); } catch { return {}; } })();
let days = [1, 7, 30].includes(pref.days) ? pref.days : 7;
let res = null;
let repoOff = new Set(); // repo keys switched off: top-3 repo names or "__other"
let palette = [];        // [{key, label, color, members:[repo names]}], from the unfiltered repo list
const SLOTS = ["--r1", "--r2", "--r3"];

function buildPalette(repos) {
  const top = repos.slice(0, 3).map((r, i) => ({ key: r.repo, label: r.repo, color: SLOTS[i], members: [r.repo] }));
  const rest = repos.slice(3).map((r) => r.repo);
  if (rest.length) top.push({ key: "__other", label: rest.length === 1 ? rest[0] : "Other (" + rest.length + ")", color: "--r-other", members: rest });
  return top;
}
const groupOf = (repo) => palette.find((g) => g.members.includes(repo));

async function load() {
  $("state").textContent = "○ loading";
  const q = new URLSearchParams({ days, tz: new Date().getTimezoneOffset() });
  if (repoOff.size) q.set("repos", palette.filter((g) => !repoOff.has(g.key)).flatMap((g) => g.members).join(","));
  try {
    const r = await fetch("/v1/stats?" + q);
    if (!r.ok) throw new Error(r.status);
    res = await r.json();
    if (!repoOff.size) palette = buildPalette(res.repos);
    $("state").textContent = "● " + new Date().toLocaleTimeString("en-GB", { hour: "2-digit", minute: "2-digit" });
    $("state").classList.add("live");
    render();
  } catch {
    $("state").textContent = "○ can't reach TRK";
    $("state").classList.remove("live");
  }
}

$("range").addEventListener("click", (e) => {
  const b = e.target.closest("[data-r]");
  if (!b) return;
  days = Number(b.dataset.r);
  try { localStorage.setItem(PREF_KEY, JSON.stringify({ days })); } catch { /* per-browser convenience only */ }
  repoOff = new Set(); // repos differ per range
  load();
});
$("repos").addEventListener("click", (e) => {
  const b = e.target.closest("[data-repo]");
  if (!b) return;
  const k = b.dataset.repo;
  if (repoOff.has(k)) repoOff.delete(k);
  else if (palette.length - repoOff.size > 1) repoOff.add(k); // keep at least one
  load();
});

// ---------- render ----------
function render() {
  for (const b of $("range").children) b.setAttribute("aria-pressed", String(Number(b.dataset.r) === days));
  $("repos").replaceChildren(...(palette.length ? palette.map((g) => h("button", { class: "st-rchip", type: "button", "data-repo": g.key, "aria-pressed": String(!repoOff.has(g.key)), title: g.members.join(", ") },
    h("i", { class: "st-sw", style: "background:var(" + g.color + ")" }), g.label)) : [h("span", { class: "st-q" }, "no spend recorded yet")]));
  drawKpis(); drawSpend(); drawSplit(); drawWaits(); drawHeat(); drawTools(); drawSlow(); drawFiles(); drawCtx(); drawSessions();
}

function drawKpis() {
  const k = res.kpi;
  const series = res.days.map((d) => Object.values(d.spend).reduce((a, b) => a + b, 0));
  const spark = document.createElementNS(NS, "svg");
  spark.setAttribute("class", "st-spark");
  spark.setAttribute("viewBox", "0 0 100 26");
  spark.setAttribute("preserveAspectRatio", "none");
  spark.setAttribute("aria-hidden", "true");
  if (series.length > 1) {
    const mx = Math.max(...series, 0.01), n = series.length - 1;
    const pts = series.map((v, i) => [(i / n) * 100, 24 - (v / mx) * 20]);
    sv("path", { d: "M0,26 " + pts.map((p) => "L" + p[0] + "," + p[1]).join(" ") + " L100,26 Z", fill: "rgba(255,90,74,0.12)" }, spark);
    sv("path", { d: "M" + pts.map((p) => p.join(",")).join(" L"), fill: "none", stroke: css("--accent"), "stroke-width": 1.5, "vector-effect": "non-scaling-stroke" }, spark);
  }
  const kpi = (lab, num, sub, extra) => h("div", { class: "st-kpi" }, h("span", { class: "k-lab" }, lab), num, extra, h("span", { class: "k-sub" }, sub));
  const big = (v, small) => h("span", { class: "k-num" }, v, small ? h("small", null, small) : null);
  const models = Object.entries(k.tokens_by_model).sort((a, b) => b[1] - a[1]);
  const modelLine = models.length ? models.slice(0, 2).map(([m, v]) => [h("b", null, Math.round((v / (k.tokens || 1)) * 100) + "%"), " " + m]).flatMap((x, i) => (i ? [" · ", x] : [x])) : "no token readings";
  const change = k.spend_prev > 0 ? Math.round((k.spend / k.spend_prev - 1) * 100) : null;
  const waitMin = Math.floor(k.wait_median / 60), waitSec = Math.round(k.wait_median % 60);
  const failPct = k.tool_calls ? (k.tool_fails / k.tool_calls) * 100 : 0;
  $("kpis").replaceChildren(
    kpi("spend", big(k.spend >= 100 ? "$" + Math.round(k.spend) : money(k.spend)),
      change == null ? (days === 1 ? "today so far" : "no spend in the " + days + " days before") : [h("b", null, (change >= 0 ? "+" : "") + change + "%"), " vs the " + days + " days before"], spark),
    kpi("tokens", big(tok(k.tokens)), modelLine),
    kpi("agent hours", big(k.agent_hours >= 10 ? Math.round(k.agent_hours) : k.agent_hours.toFixed(1), "h"), [h("b", null, String(k.peak_agents)), " agent" + (k.peak_agents === 1 ? "" : "s") + " at once at peak"]),
    kpi("you answer in", k.wait_median ? big(waitMin ? waitMin + "m" : waitSec + "s", waitMin ? String(waitSec).padStart(2, "0") + "s" : null) : big("—"),
      res.waits.length ? ["median · slowest 10% ", h("b", null, dur(k.wait_p90) + "+")] : "no prompts waited on you"),
    kpi("tool failures", big(failPct.toFixed(1), "%"), [h("b", null, String(k.tool_fails)), " of " + k.tool_calls + " tool calls"]));
}

function width(id, min) { return Math.max(min, $(id).clientWidth || min); }

function drawSpend() {
  const groups = palette.filter((g) => !repoOff.has(g.key));
  const byGroup = (d, g) => g.members.reduce((a, m) => a + (d.spend[m] || 0), 0);
  const totals = res.days.map((d) => groups.reduce((a, g) => a + byGroup(d, g), 0));
  if (!totals.some((t) => t > 0)) { $("spend").replaceChildren(none("No spend recorded in this range.")); $("spend-legend").replaceChildren(); return; }
  const W = width("spend", 320), H = 230, L = 46, R = 8, T = 16, B = 26;
  const svg = document.createElementNS(NS, "svg");
  svg.setAttribute("viewBox", `0 0 ${W} ${H}`);
  svg.setAttribute("role", "img");
  svg.setAttribute("aria-label", "Spend per day by repo");
  svg.style.height = H + "px";
  const peak = Math.max(...totals);
  const step = [1, 2, 5, 10, 20, 25, 50, 100, 200, 500].find((s) => s * 4 >= peak) || Math.ceil(peak / 4);
  const top = step * 4;
  const y = (v) => T + (H - T - B) * (1 - v / top);
  for (let v = 0; v <= top; v += step) {
    sv("line", { class: "st-gl", x1: L, x2: W - R, y1: y(v), y2: y(v) }, svg);
    sv("text", { class: "st-ax", x: L - 8, y: y(v) + 4, "text-anchor": "end" }, svg, "$" + v);
  }
  const n = res.days.length, slot = (W - L - R) / n, bw = Math.min(46, slot * 0.62);
  const every = n <= 10 ? 1 : 5;
  res.days.forEach((d, i) => {
    const x = L + slot * i + (slot - bw) / 2;
    let acc = 0;
    const parts = groups.map((g) => [g, byGroup(d, g)]).filter(([, v]) => v > 0);
    parts.forEach(([g, v], j) => {
      const y0 = y(acc), y1 = y(acc + v);
      sv("rect", { x, y: y1, width: bw, height: Math.max(0.5, y0 - y1 - (j ? 2 : 0)), fill: css(g.color), rx: j === parts.length - 1 ? 3 : 0 }, svg);
      acc += v;
    });
    const date = new Date(d.start);
    if (i % every === every - 1 || i === n - 1 || n <= 10) {
      sv("text", { class: "st-ax", x: x + bw / 2, y: H - 8, "text-anchor": "middle" }, svg,
        n === 1 ? "today" : date.toLocaleDateString("en-GB", n <= 7 ? { weekday: "short", day: "numeric" } : { day: "numeric", month: "short" }));
    }
    if (totals[i] > 0 && (i === n - 1 || totals[i] === peak)) sv("text", { class: "st-lbl", x: x + bw / 2, y: y(totals[i]) - 6, "text-anchor": "middle" }, svg, money(totals[i]));
    hover(sv("rect", { class: "st-hit", x: L + slot * i, y: T, width: slot, height: H - T - B }, svg), () => [
      h("div", null, date.toLocaleDateString("en-GB", { weekday: "long", day: "numeric", month: "short" })), h("div", null, h("b", null, money(totals[i]))),
      ...parts.map(([g, v]) => trow(css(g.color), g.label, money(v)))]);
  });
  $("spend").replaceChildren(svg);
  $("spend-legend").replaceChildren(...groups.map((g) => h("span", null, h("i", { class: "st-sw", style: "background:var(" + g.color + ")" }),
    g.label + " " + money(res.days.reduce((a, d) => a + byGroup(d, g), 0)))));
}

const label = (s) => s.name + " · " + new Date(s.first).toLocaleDateString("en-GB", { day: "numeric", month: "short" });

function drawSplit() {
  const rows = res.sessions.filter((s) => s.model_ms + s.tool_ms + s.wait_ms > 0)
    .sort((a, b) => (b.model_ms + b.tool_ms + b.wait_ms) - (a.model_ms + a.tool_ms + a.wait_ms)).slice(0, 6);
  if (!rows.length) return $("split").replaceChildren(none("No session time recorded in this range."));
  const row = (name, m, t, w, cls) => {
    const all = m + t + w || 1;
    const bar = h("div", { class: "st-sbar" }, h("i", { style: `width:${(m / all) * 100}%;background:var(--t-model)` }),
      h("i", { style: `width:${(t / all) * 100}%;background:var(--t-tools)` }), h("i", { style: `width:${(w / all) * 100}%;background:var(--t-wait)` }));
    hover(bar, () => [h("div", null, name), trow(css("--t-model"), "model working", dur(m / 1000)), trow(css("--t-tools"), "tools running", dur(t / 1000)),
      trow(css("--t-wait"), "waiting on you", dur(w / 1000))]);
    return h("div", { class: "st-srow " + (cls || "") }, h("span", { class: "one", title: name }, name), bar, h("span", { class: "v" }, Math.round((w / all) * 100) + "% you"));
  };
  const sum = (k) => res.sessions.reduce((a, s) => a + s[k], 0);
  $("split").replaceChildren(...rows.map((s) => row(label(s), s.model_ms, s.tool_ms, s.wait_ms)),
    row("All sessions", sum("model_ms"), sum("tool_ms"), sum("wait_ms"), "total"));
}

function drawWaits() {
  const W = res.waits;
  if (!W.length) { $("waits").replaceChildren(none("Nothing waited on you in this range.")); $("wait-insight").hidden = true; return; }
  const BK = [[0, 30, "<30s"], [30, 120, "30s–2m"], [120, 300, "2–5m"], [300, 900, "5–15m"], [900, 1800, "15–30m"], [1800, Infinity, "30m+"]];
  const counts = BK.map(([a, b]) => W.filter((w) => w >= a && w < b).length);
  const Wd = width("waits", 280), H = 170, L = 8, B = 24, T = 18;
  const svg = document.createElementNS(NS, "svg");
  svg.setAttribute("viewBox", `0 0 ${Wd} ${H}`);
  svg.style.height = H + "px";
  svg.setAttribute("role", "img");
  svg.setAttribute("aria-label", "How long prompts waited for you");
  const mx = Math.max(...counts, 1), slot = (Wd - L * 2) / BK.length, bw = slot * 0.7;
  counts.forEach((c, i) => {
    const x = L + slot * i + (slot - bw) / 2, hh = ((H - T - B) * c) / mx;
    if (c) sv("rect", { x, y: H - B - hh, width: bw, height: hh, rx: 3, fill: i >= 4 ? css("--t-wait") : "rgba(255,178,122,0.45)" }, svg);
    sv("text", { class: "st-lbl", x: x + bw / 2, y: H - B - hh - 5, "text-anchor": "middle" }, svg, String(c));
    sv("text", { class: "st-ax", x: x + bw / 2, y: H - 7, "text-anchor": "middle" }, svg, BK[i][2]);
    hover(sv("rect", { class: "st-hit", x: L + slot * i, y: 0, width: slot, height: H - B }, svg), () => [h("div", null, "Answered in " + BK[i][2]),
      h("div", null, h("b", null, String(c)), " prompts · " + Math.round((c / W.length) * 100) + "%")]);
  });
  $("waits").replaceChildren(svg);
  const slow = W.filter((w) => w >= 900);
  $("wait-insight").hidden = !slow.length;
  $("wait-insight").replaceChildren("Agents sat idle ", h("b", null, dur(slow.reduce((a, b) => a + b, 0))), " on ", h("b", null, String(slow.length)),
    " prompt" + (slow.length === 1 ? "" : "s") + " you answered after 15+ minutes.");
}

function drawHeat() {
  const days7 = ["Mon", "Tue", "Wed", "Thu", "Fri", "Sat", "Sun"];
  const mx = Math.max(...res.heat.flat(), 1);
  const color = (v) => (v === 0 ? css("--track") : `rgba(255,90,74,${(0.18 + 0.82 * (v / mx)).toFixed(2)})`);
  const kids = [h("span"), ...Array.from({ length: 24 }, (_, hh) => h("span", { class: "hl" }, hh % 3 === 0 ? String(hh).padStart(2, "0") : ""))];
  res.heat.forEach((row, di) => {
    kids.push(h("span", { class: "dl" }, days7[di]));
    row.forEach((v, hh) => {
      const c = h("span", { class: "c", style: "background:" + color(v) });
      hover(c, () => [h("div", null, days7[di] + " " + String(hh).padStart(2, "0") + ":00"), h("div", null, h("b", null, String(v)), " tool calls")]);
      kids.push(c);
    });
  });
  $("heat").replaceChildren(...kids);
  const byHour = Array.from({ length: 24 }, (_, hh) => res.heat.reduce((a, r) => a + r[hh], 0));
  const best = byHour.indexOf(Math.max(...byHour));
  $("heat-legend").replaceChildren(h("span", null, "fewer"), ...[0, 0.25, 0.5, 0.75, 1].map((f) => h("i", { class: "st-sw", style: "background:" + color(f * mx) })),
    h("span", null, "more" + (byHour[best] ? " · busiest hour: " + String(best).padStart(2, "0") + ":00" : "")));
}

function drawTools() {
  if (!res.tools.length) return $("tools").replaceChildren(none("No tool calls in this range."));
  const mx = res.tools[0].calls;
  $("tools").replaceChildren(...res.tools.map((t) => {
    const ok = t.calls - t.fails;
    const tr = h("div", { class: "st-btrack" }, ok ? h("i", { style: `width:${(ok / mx) * 100}%;background:var(--accent);box-shadow:0 0 6px rgba(255,90,74,.45)` }) : null,
      t.fails ? h("i", { class: "st-sw-fail", style: `width:${(t.fails / mx) * 100}%;min-width:3px` }) : null);
    hover(tr, () => [h("div", null, t.name), h("div", null, h("b", null, String(t.calls)), " calls"), trow(css("--red"), "failed", t.fails + (t.fails ? " · " + ((t.fails / t.calls) * 100).toFixed(1) + "%" : ""))]);
    return h("div", { class: "st-brow" }, h("span", { class: "name", title: t.name }, t.name), tr,
      h("span", { class: "v" }, String(t.calls), t.fails ? [" · ", h("span", { class: "st-fail" }, "✕" + t.fails)] : null));
  }));
}

function sortable(table, cols, rows, state, cell, onSort) {
  const head = h("tr", null, cols.map(([name, key, cls]) => h("th", { class: cls || "", scope: "col" }, key == null ? name :
    h("button", { type: "button", "data-sort": key, "aria-sort": state[0] === key ? (state[1] > 0 ? "ascending" : "descending") : null },
      name + (state[0] === key ? (state[1] > 0 ? " ▲" : " ▼") : "")))));
  const sorted = [...rows].sort((a, b) => (a[state[0]] > b[state[0]] ? 1 : a[state[0]] < b[state[0]] ? -1 : 0) * state[1]);
  table.replaceChildren(h("thead", null, head), h("tbody", null, sorted.map(cell)));
  table.onclick = (e) => { const b = e.target.closest("[data-sort]"); if (b) onSort(b.dataset.sort); };
}

let slowSort = ["median_ms", -1];
function drawSlow() {
  if (!res.commands.length) return $("slow").replaceChildren(h("tbody", null, h("tr", null, h("td", null, none("No Bash commands in this range.")))));
  const mx = Math.max(...res.commands.map((c) => c.p95_ms), 1);
  sortable($("slow"), [["Command", "cmd"], ["Runs", "runs", "num"], ["Median", "median_ms", "num"], ["Slowest 5%", "p95_ms", "num"], ["Failed", "fails", "num"], ["", null]],
    res.commands, slowSort, (c) => h("tr", null, h("td", null, h("code", null, c.cmd)), h("td", { class: "num" }, String(c.runs)),
      h("td", { class: "num" }, dur(c.median_ms / 1000)), h("td", { class: "num" }, dur(c.p95_ms / 1000)),
      h("td", { class: "num" + (c.fails ? " st-fail" : "") }, c.fails ? Math.round((c.fails / c.runs) * 100) + "%" : "—"),
      h("td", { style: "width:32%" }, h("span", { class: "st-mini", style: `width:${(c.median_ms / mx) * 100}%` }),
        h("span", { class: "st-mini p95", style: `width:${((c.p95_ms - c.median_ms) / mx) * 100}%` }))),
    (k) => { slowSort = [k, slowSort[0] === k ? -slowSort[1] : -1]; drawSlow(); });
}

function drawFiles() {
  if (!res.files.length) return $("files").replaceChildren(none("No files read or edited in this range."));
  const mx = Math.max(...res.files.map((f) => f.reads + f.edits));
  const short = (p) => p.split("/").filter(Boolean).slice(-2).join("/");
  $("files").replaceChildren(...res.files.map((f) => {
    const tr = h("div", { class: "st-btrack" }, f.reads ? h("i", { style: `width:${(f.reads / mx) * 100}%;background:var(--t-model)` }) : null,
      f.edits ? h("i", { style: `width:${(f.edits / mx) * 100}%;background:var(--accent)` }) : null);
    hover(tr, () => [h("div", null, f.path), trow(css("--t-model"), "read", String(f.reads)), trow(css("--accent"), "edited", String(f.edits)), trow(null, "agents", String(f.agents))]);
    return h("div", { class: "st-brow" }, h("span", { class: "name", title: f.path }, short(f.path)), tr,
      h("span", { class: "v" }, String(f.reads + f.edits) + (f.agents > 1 ? " · " + f.agents + " agents" : "")));
  }));
}

const ctxColor = (p) => (p >= 60 ? css("--red") : p >= 40 ? css("--yellow") : css("--ok"));
function drawCtx() {
  const rows = res.sessions.filter((s) => s.peak_ctx != null).sort((a, b) => b.peak_ctx - a.peak_ctx).slice(0, 10);
  if (!rows.length) { $("ctx").replaceChildren(none("No context readings in this range.")); $("ctx-insight").hidden = true; return; }
  const W = width("ctx", 300), H = 30 * rows.length + 30, L = Math.min(170, W * 0.38), R = 44;
  const svg = document.createElementNS(NS, "svg");
  svg.setAttribute("viewBox", `0 0 ${W} ${H}`);
  svg.style.height = H + "px";
  svg.setAttribute("role", "img");
  svg.setAttribute("aria-label", "Fullest context per session");
  const x = (p) => L + ((W - L - R) * Math.min(p, 100)) / 100;
  sv("rect", { x: x(40), y: 0, width: x(60) - x(40), height: H - 22, fill: "rgba(255,214,107,0.05)" }, svg);
  sv("rect", { x: x(60), y: 0, width: x(100) - x(60), height: H - 22, fill: "rgba(255,74,61,0.07)" }, svg);
  for (const p of [0, 40, 60, 100]) {
    sv("line", { class: "st-gl", x1: x(p), x2: x(p), y1: 0, y2: H - 22 }, svg);
    sv("text", { class: "st-ax", x: x(p), y: H - 6, "text-anchor": "middle" }, svg, p + "%");
  }
  const chars = Math.max(8, Math.floor(L / 7.2));
  rows.forEach((s, i) => {
    const yy = 14 + i * 30, col = ctxColor(s.peak_ctx), name = label(s);
    sv("text", { class: "st-ax", x: L - 10, y: yy + 4, "text-anchor": "end" }, svg, name.length > chars ? name.slice(0, chars - 1) + "…" : name);
    sv("line", { x1: x(0), x2: x(s.peak_ctx), y1: yy, y2: yy, stroke: col, "stroke-width": 2, opacity: 0.6 }, svg);
    sv("circle", { cx: x(s.peak_ctx), cy: yy, r: 5, fill: css("--panel"), stroke: col, "stroke-width": 2.5 }, svg);
    sv("text", { class: "st-lbl", x: x(s.peak_ctx) + 10, y: yy + 4 }, svg, Math.round(s.peak_ctx) + "%");
    hover(sv("rect", { class: "st-hit", x: 0, y: yy - 15, width: W, height: 30 }, svg), () => [h("div", null, name), h("div", null, h("b", null, Math.round(s.peak_ctx) + "%"), " fullest context")]);
  });
  $("ctx").replaceChildren(svg);
  const all = res.sessions.filter((s) => s.peak_ctx != null), hot = all.filter((s) => s.peak_ctx >= 60).length;
  $("ctx-insight").hidden = !hot;
  $("ctx-insight").replaceChildren(h("b", null, hot + " of " + all.length), " sessions went past 60% context, where answers get worse. Hand off or /compact earlier.");
}

let sesSort = ["spend", -1];
function drawSessions() {
  if (!res.sessions.length) return $("sessions").replaceChildren(h("tbody", null, h("tr", null, h("td", null, none("No sessions in this range.")))));
  const rows = res.sessions.map((s) => ({ ...s, duration: s.last - s.first, waitShare: s.wait_ms / (s.model_ms + s.tool_ms + s.wait_ms || 1) }));
  sortable($("sessions"), [["Session", "name"], ["Started", "first"], ["Repo", "repo"], ["Model", "model"], ["Duration", "duration", "num"], ["Cost", "spend", "num"],
    ["Tokens", "tokens", "num"], ["Waiting on you", "waitShare", "num"], ["Peak context", "peak_ctx", "num"]], rows, sesSort, (s) => {
    const g = groupOf(s.repo);
    return h("tr", null, h("td", { title: s.id }, s.name), h("td", null, when(s.first)),
      h("td", null, h("i", { class: "st-sw", style: "background:var(" + (g ? g.color : "--r-other") + ");margin-right:6px" }), s.repo),
      h("td", null, s.model || "—"), h("td", { class: "num" }, dur(s.duration / 1000)), h("td", { class: "num" }, money(s.spend)), h("td", { class: "num" }, tok(s.tokens)),
      h("td", { class: "num" }, Math.round(s.waitShare * 100) + "%"),
      h("td", { class: "num", style: s.peak_ctx != null ? "color:" + ctxColor(s.peak_ctx) : null }, s.peak_ctx != null ? Math.round(s.peak_ctx) + "%" : "—"));
  }, (k) => { sesSort = [k, sesSort[0] === k ? -sesSort[1] : -1]; drawSessions(); });
}

$("host").textContent = location.host;
load();
setInterval(() => { if (tip.hidden) load(); }, 60e3); // history: a refresh a minute is plenty
let rz;
addEventListener("resize", () => { clearTimeout(rz); rz = setTimeout(() => res && render(), 120); });
addEventListener("scroll", hideTip, { passive: true });

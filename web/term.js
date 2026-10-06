"use strict";
// A viewer for one dashboard-started agent (daemon pty → xterm.js over a websocket).
// Shared by the agent drawer (index.html) and the pop-out page (term.html).
// Builds DOM with createElement/textContent only, like app.js.

window.TRKTerm = (function () {
  const THEME = {
    background: "#0B0605", foreground: "#F1DCD6", cursor: "#FF5A4A", cursorAccent: "#0B0605",
    selectionBackground: "#6E2A20", black: "#110908", red: "#FF4A3D", green: "#5CFF9D", yellow: "#FFD66B",
    blue: "#7FB2FF", magenta: "#FF7A6B", cyan: "#8FE3D6", white: "#F1DCD6", brightBlack: "#7A5650",
  };

  // Font size is one setting for every terminal view, kept in this browser; open views
  // (other tabs and pop-outs included) follow a change.
  const FONT_KEY = "trk.termFont", FONT_MIN = 10, FONT_MAX = 24, FONT_DEF = 14;
  const views = new Set();
  function fontSize() {
    let n = 0;
    try { n = Number(localStorage.getItem(FONT_KEY)); } catch { /* storage off */ }
    return n >= FONT_MIN && n <= FONT_MAX ? n : FONT_DEF;
  }
  function applyFont(n) { for (const v of views) v.setFont(n); }
  function setFontSize(n) {
    n = Math.max(FONT_MIN, Math.min(FONT_MAX, Math.round(n)));
    try { localStorage.setItem(FONT_KEY, String(n)); } catch { /* storage off: this page only */ }
    applyFont(n);
    return n;
  }
  addEventListener("storage", (e) => { if (e.key === FONT_KEY) applyFont(fontSize()); });

  // attach shows terminal `id` inside `el` and keeps it connected until dispose().
  // A dropped connection (lagging viewer, daemon hiccup) reconnects and replays the scrollback.
  function attach(el, id, { onExit, onState } = {}) {
    const xterm = new Terminal({ fontFamily: '"IBM Plex Mono", ui-monospace, monospace', fontSize: fontSize(), cursorBlink: true,
      scrollback: 5000, theme: THEME });
    const fit = new FitAddon.FitAddon();
    xterm.loadAddon(fit);
    xterm.open(el);
    let ws = null, closed = false, exited = false, retry = 0;
    const enc = new TextEncoder();
    const send = (d) => ws && ws.readyState === 1 && ws.send(d);
    const size = () => { try { fit.fit(); } catch { /* hidden */ } send(JSON.stringify({ cols: xterm.cols, rows: xterm.rows })); };
    xterm.onData((d) => send(enc.encode(d)));
    xterm.onBinary((d) => send(Uint8Array.from(d, (c) => c.charCodeAt(0))));
    const ro = new ResizeObserver(size);
    ro.observe(el);

    function connect() {
      ws = new WebSocket(`${location.protocol === "https:" ? "wss" : "ws"}://${location.host}/v1/terms/${encodeURIComponent(id)}/ws`);
      ws.binaryType = "arraybuffer";
      ws.onopen = () => { retry = 0; xterm.reset(); size(); onState && onState("live"); };
      ws.onmessage = (e) => {
        if (typeof e.data === "string") {
          if (e.data.includes('"exit"')) { exited = true; xterm.write("\r\n\x1b[33m[claude exited]\x1b[0m\r\n"); onExit && onExit(); }
          return;
        }
        xterm.write(new Uint8Array(e.data));
      };
      ws.onclose = () => {
        if (closed || exited) return;
        onState && onState("reconnecting");
        setTimeout(() => !closed && connect(), Math.min(4000, 300 * 2 ** retry++));
      };
    }
    connect();
    const view = {
      focus: () => xterm.focus(),
      fit: size,
      setFont(n) { if (xterm.options.fontSize !== n) { xterm.options.fontSize = n; size(); } },
      dispose() { closed = true; views.delete(view); ro.disconnect(); if (ws) ws.close(); xterm.dispose(); },
    };
    views.add(view);
    return view;
  }

  async function stop(id) { await fetch("/v1/terms/" + encodeURIComponent(id), { method: "DELETE" }); }

  function popOut(t) {
    return window.open("term.html?id=" + encodeURIComponent(t.id), "trk-term-" + t.id, "popup,width=1100,height=720");
  }

  // fontControl wires a "A− 14 A+" group: buttons [data-font="-1|1"], value in [data-font-n].
  function fontControl(el) {
    const show = () => { el.querySelector("[data-font-n]").textContent = fontSize() + "px"; };
    el.addEventListener("click", (e) => {
      const b = e.target.closest("[data-font]");
      if (b) { setFontSize(fontSize() + Number(b.dataset.font)); show(); }
    });
    el.addEventListener("dblclick", (e) => { if (e.target.closest("[data-font-n]")) { setFontSize(FONT_DEF); show(); } });
    addEventListener("storage", (e) => { if (e.key === FONT_KEY) show(); });
    show();
  }

  return { attach, stop, popOut, fontControl };
})();

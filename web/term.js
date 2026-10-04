"use strict";
// Experimental: a viewer for one dashboard-started agent (daemon pty → xterm.js over a
// websocket). Shared by the New agent modal (index.html) and the pop-out page (term.html).
// Builds DOM with createElement/textContent only, like app.js.

window.TRKTerm = (function () {
  const THEME = {
    background: "#0B0605", foreground: "#F1DCD6", cursor: "#FF5A4A", cursorAccent: "#0B0605",
    selectionBackground: "#6E2A20", black: "#110908", red: "#FF4A3D", green: "#5CFF9D", yellow: "#FFD66B",
    blue: "#7FB2FF", magenta: "#FF7A6B", cyan: "#8FE3D6", white: "#F1DCD6", brightBlack: "#7A5650",
  };

  // attach shows terminal `id` inside `el` and keeps it connected until dispose().
  // A dropped connection (lagging viewer, daemon hiccup) reconnects and replays the scrollback.
  function attach(el, id, { onExit, onState } = {}) {
    const xterm = new Terminal({ fontFamily: '"IBM Plex Mono", ui-monospace, monospace', fontSize: 14, cursorBlink: true,
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
    return {
      focus: () => xterm.focus(),
      fit: size,
      dispose() { closed = true; ro.disconnect(); if (ws) ws.close(); xterm.dispose(); },
    };
  }

  async function stop(id) { await fetch("/v1/terms/" + encodeURIComponent(id), { method: "DELETE" }); }

  function popOut(t) {
    return window.open("term.html?id=" + encodeURIComponent(t.id), "trk-term-" + t.id, "popup,width=1100,height=720");
  }

  return { attach, stop, popOut };
})();

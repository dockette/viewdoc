(() => {
  const tabs = document.getElementById('slot-tabs');
  const viewer = document.getElementById('viewer');
  const form = document.getElementById('url-form');
  const input = document.getElementById('url-input');
  const resetBtn = document.getElementById('url-reset');
  const restartBtn = document.getElementById('slot-restart');

  // Surface iframe console messages on the outer page so a single DevTools
  // console shows everything across slots. The shim in proxy.go posts these.
  window.addEventListener('message', (e) => {
    const d = e.data;
    if (!d || d._viewdocConsole !== true) return;
    if (e.origin !== location.origin) return;
    const fn = (console[d.level] || console.log).bind(console);
    fn(`[${d.slot}]`, ...(d.args || []));
  });

  const params = new URLSearchParams(location.search);
  const rawSlot = (params.get('slot') || '0').toLowerCase();
  // `?slot=auto` defers to a random ready slot once /api/slots returns.
  let activeSlot = rawSlot === 'auto' ? -1 : parseInt(rawSlot, 10);
  if (Number.isNaN(activeSlot)) activeSlot = 0;
  let slots = [];

  function pickRandomReadySlot() {
    const ready = slots.filter((s) => s.ready);
    const pool = ready.length ? ready : slots;
    if (!pool.length) return 0;
    return pool[Math.floor(Math.random() * pool.length)].idx;
  }

  // Frames are kept alive across switches so KasmVNC keeps its single
  // primary client and the iframe never blinks/reconnects.
  const slotState = new Map();

  function getSlotState(idx) {
    let s = slotState.get(idx);
    if (!s) { s = { frame: null, state: 'idle' }; slotState.set(idx, s); }
    return s;
  }

  function setSlotState(idx, state) {
    const s = getSlotState(idx);
    if (s.state === state) return;
    s.state = state;
    renderTabs();
  }

  function ensureFrame(idx) {
    const s = getSlotState(idx);
    if (s.frame) return s.frame;
    const slot = slots[idx];
    const f = document.createElement('iframe');
    f.dataset.slot = String(idx);
    // Same-origin with the parent so user activation propagates into the
    // iframe — without it, the suspended AudioContext inside KasmVNC /
    // Selkies never resumes and audio stays silent. resize=remote asks the
    // client to call SetDesktopSize so the inner canvas matches the iframe.
    // The iframe itself is CSS-capped at 1920x1080 (see style.css) so the
    // resize follows the host window but never exceeds that — saves both
    // backends from rendering 4K framebuffers on big monitors.
    f.src = `${slot.path}?resize=remote`;
    f.allow = 'autoplay; clipboard-read; clipboard-write; fullscreen';
    f.addEventListener('load', () => {
      const cur = getSlotState(idx);
      if (cur.state !== 'down') setSlotState(idx, 'connected');
    });
    viewer.appendChild(f);
    s.frame = f;
    s.state = 'loading';
    return f;
  }

  async function loadSlots() {
    let next;
    try {
      const r = await fetch('/api/slots');
      const data = await r.json();
      next = data.slots || [];
    } catch {
      return;
    }
    slots = next;
    slots.forEach((s) => {
      const cur = getSlotState(s.idx);
      if (cur.state === 'restarting') {
        trackRestart(s);
        return;
      }
      if (!s.ready) {
        if (cur.state !== 'down') setSlotState(s.idx, 'down');
      } else if (cur.state === 'down') {
        setSlotState(s.idx, cur.frame ? 'connected' : 'idle');
      }
    });
    renderTabs();
  }

  // A restart is done once the slot has gone down and come back up — reloading
  // the iframe any earlier would reconnect to the dying desktop. The timeout
  // covers a restart that never took the slot down (old image without the
  // restart wrapper) so the tab doesn't blink forever.
  const RESTART_TIMEOUT_MS = 60000;

  function trackRestart(s) {
    const cur = getSlotState(s.idx);
    if (!s.ready) cur.sawDown = true;
    const done = s.ready && (cur.sawDown || Date.now() - cur.restartAt > RESTART_TIMEOUT_MS);
    if (!done) return;
    cur.state = 'idle';
    if (s.idx === activeSlot) ensureFrame(s.idx).classList.add('active');
  }

  async function restartSlot(idx) {
    const cur = getSlotState(idx);
    if (cur.state === 'restarting') return;
    if (!confirm(`Restart slot ${idx + 1}? Anything open in it will be closed.`)) return;
    console.log(`[viewdoc] restart slot=${idx}`);
    try {
      const r = await fetch(`/api/slot/${idx}/restart`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: '{}',
      });
      if (!r.ok) {
        console.warn(`[viewdoc] restart slot=${idx} HTTP ${r.status}`);
        return;
      }
    } catch (e) {
      console.error(`[viewdoc] restart slot=${idx} failed`, e);
      return;
    }
    // Drop the iframe now so the websocket closes cleanly instead of leaving
    // a stale KasmVNC primary-client lock behind.
    if (cur.frame) {
      cur.frame.remove();
      cur.frame = null;
    }
    cur.sawDown = false;
    cur.restartAt = Date.now();
    setSlotState(idx, 'restarting');
    const poll = setInterval(async () => {
      await loadSlots();
      if (getSlotState(idx).state !== 'restarting') clearInterval(poll);
    }, 2000);
  }

  function renderTabs() {
    tabs.replaceChildren();
    slots.forEach((s) => {
      const b = document.createElement('button');
      b.type = 'button';
      b.className = `tab tab-${getSlotState(s.idx).state}`;
      if (s.idx === activeSlot) b.classList.add('active');
      b.title = s.ip ? `${s.host} · ${s.ip}` : s.host;

      const led = document.createElement('span');
      led.className = 'led';
      b.appendChild(led);

      const label = document.createElement('span');
      label.className = 'tab-label';
      label.textContent = String(s.idx + 1);
      b.appendChild(label);

      b.addEventListener('click', () => selectSlot(s.idx));
      tabs.appendChild(b);
    });
  }

  function selectSlot(i) {
    if (i < 0 || i >= slots.length) i = 0;
    activeSlot = i;
    slotState.forEach((s, idx) => {
      if (!s.frame) return;
      s.frame.classList.toggle('active', idx === i);
    });
    // A restarting slot gets its frame back from trackRestart once it's up.
    if (getSlotState(i).state !== 'restarting') ensureFrame(i).classList.add('active');

    renderTabs();

    const u = new URL(location.href);
    u.searchParams.set('slot', String(i));
    history.replaceState(null, '', u);

    const docURL = u.searchParams.get('url');
    if (docURL) pushParams({ url: docURL });
  }

  async function pushParams(p) {
    console.log(`[viewdoc] push slot=${activeSlot}`, p);
    try {
      const r = await fetch(`/api/slot/${activeSlot}/params`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ params: p }),
      });
      if (!r.ok) console.warn(`[viewdoc] push slot=${activeSlot} HTTP ${r.status}`);
    } catch (e) {
      console.error(`[viewdoc] push slot=${activeSlot} failed`, e);
    }
  }

  form.addEventListener('submit', (e) => {
    e.preventDefault();
    const url = input.value.trim();
    if (!url) return;
    const u = new URL(location.href);
    u.searchParams.set('url', url);
    history.replaceState(null, '', u);
    pushParams({ url });
  });

  resetBtn.addEventListener('click', () => {
    input.value = '';
    const u = new URL(location.href);
    u.searchParams.delete('url');
    history.replaceState(null, '', u);
    input.focus();
  });

  restartBtn.addEventListener('click', () => restartSlot(activeSlot));

  const initialURL = params.get('url');
  if (initialURL) input.value = initialURL;

  loadSlots().then(() => {
    if (activeSlot < 0) activeSlot = pickRandomReadySlot();
    selectSlot(activeSlot);
  });
  setInterval(loadSlots, 5000);
})();

// Cook mode: stepper, swipe, wake lock, timers with pre-alerts, ingredients tick-off (spec 70 sections 4.4, 5).
import { actions, toast } from './app.js';
import { t } from './lib/i18n.js';
import { normalizeKeys, readJSON } from './lib/keys.js';
import * as T from './lib/timers.js';

const cfg = normalizeKeys(readJSON('cook-config') || {});
const root = document.querySelector('.cook-root');
if (root) init();

function init() {
  const $ = (s, r = root) => r.querySelector(s);
  const $$ = (s, r = root) => [...r.querySelectorAll(s)];
  const rtl = getComputedStyle(document.documentElement).direction === 'rtl';
  const recipeId = root.dataset.recipeId;

  /* ---------- steps ---------- */
  const steps = $$('.cook-step');
  let cur = 0;
  function showStep(i, { updateHash = true } = {}) {
    cur = Math.max(0, Math.min(steps.length - 1, i));
    steps.forEach((s, k) => s.classList.toggle('is-current', k === cur));
    const c = $('[data-step-counter]');
    if (c) c.textContent = t('js.cook.step_of', { n: cur + 1, total: steps.length });
    const last = cur === steps.length - 1;
    const nl = $('[data-next-label]');
    if (nl) nl.textContent = last ? nl.dataset.labelFinish : nl.dataset.labelNext;
    $('[data-action="step-back"]').disabled = cur === 0;
    if (updateHash) { try { history.replaceState(null, '', `#step-${cur + 1}`); } catch { /* ignore */ } }
  }
  const next = () => { if (cur >= steps.length - 1) openDone(); else showStep(cur + 1); };
  const back = () => showStep(cur - 1);
  function fromHash() {
    const m = /^#step-(\d+)$/.exec(location.hash);
    if (!m) return 0;
    const idx = steps.findIndex((s) => s.id === `step-${m[1]}`);
    return idx >= 0 ? idx : 0;
  }
  showStep(fromHash(), { updateHash: false });
  addEventListener('hashchange', () => showStep(fromHash(), { updateHash: false }));

  document.addEventListener('keydown', (e) => {
    if (e.target.matches('input,textarea,select') || document.querySelector('dialog[open]')) return;
    if (e.key === 'ArrowRight') (rtl ? back : next)();
    else if (e.key === 'ArrowLeft') (rtl ? next : back)();
  });
  let sx = 0; let sy = 0; let tracking = false;
  const area = $('[data-swipe-area]');
  area.addEventListener('touchstart', (e) => { if (e.touches.length !== 1) { tracking = false; return; } sx = e.touches[0].clientX; sy = e.touches[0].clientY; tracking = true; }, { passive: true });
  area.addEventListener('touchend', (e) => {
    if (!tracking) return; tracking = false;
    const p = e.changedTouches[0]; const dx = p.clientX - sx; const dy = p.clientY - sy;
    if (Math.abs(dx) < 60 || Math.abs(dx) < Math.abs(dy) * 1.5) return;
    const forward = rtl ? dx > 0 : dx < 0;
    (forward ? next : back)();
  }, { passive: true });

  /* ---------- stages ---------- */
  const stageEls = $$('.cook-stage');
  let stageIdx = 0;
  function showStage(i) {
    if (!stageEls.length) return;
    stageIdx = (i + stageEls.length) % stageEls.length;
    stageEls.forEach((s, k) => s.classList.toggle('is-current', k === stageIdx));
    const c = $('[data-stage-counter]');
    if (c) c.textContent = t('js.cook.stage_of', { n: stageIdx + 1, total: stageEls.length });
  }

  /* ---------- wake lock ---------- */
  let lock = null;
  async function acquire() {
    if (!('wakeLock' in navigator) || document.visibilityState !== 'visible') { hintWake(!('wakeLock' in navigator)); return; }
    try { lock = await navigator.wakeLock.request('screen'); lock.addEventListener?.('release', () => { lock = null; }); }
    catch { hintWake(true); }
  }
  function hintWake(show) {
    if (!show) return;
    try { if (localStorage.getItem('nwr_wake_hint')) return; localStorage.setItem('nwr_wake_hint', '1'); } catch { /* ignore */ }
    toast(t('js.wake.hint'), 'warning');
  }
  document.addEventListener('visibilitychange', () => { if (document.visibilityState === 'visible' && !lock) acquire(); });
  addEventListener('pagehide', () => { lock?.release?.(); });
  acquire();

  /* ---------- audio / vibration ---------- */
  let ctx = null;
  function unlockAudio() {
    try {
      ctx = ctx || new (window.AudioContext || window.webkitAudioContext)();
      if (ctx.state === 'suspended') ctx.resume();
    } catch { ctx = null; }
  }
  function chime() {
    if (!ctx) return;
    try {
      const now = ctx.currentTime;
      [880, 660, 880].forEach((f, i) => {
        const o = ctx.createOscillator(); const g = ctx.createGain();
        o.type = 'sine'; o.frequency.value = f; o.connect(g); g.connect(ctx.destination);
        const t0 = now + i * 0.28;
        g.gain.setValueAtTime(0.0001, t0); g.gain.exponentialRampToValueAtTime(0.35, t0 + 0.03); g.gain.exponentialRampToValueAtTime(0.0001, t0 + 0.25);
        o.start(t0); o.stop(t0 + 0.27);
      });
    } catch { /* ignore */ }
  }
  const vibrate = () => { try { navigator.vibrate?.([400, 200, 400, 200, 400]); } catch { /* ignore */ } };

  /* ---------- timers ---------- */
  let timers = T.load(localStorage, Date.now());
  const tray = $('#timer-tray'); const banner = $('#timer-banner');
  let lastChime = 0;
  const persist = () => T.save(localStorage, timers);

  function renderTray() {
    const now = Date.now();
    tray.hidden = timers.length === 0;
    const nodes = timers.map((tm) => {
      const li = document.createElement('div');
      li.className = `timer-item${tm.firedEnd ? ' is-done' : ''}`; li.dataset.id = tm.id; li.dataset.test = 'timer-item';
      const lab = document.createElement('span'); lab.className = 't-label'; lab.textContent = tm.label; lab.dir = 'auto';
      const left = document.createElement('span'); left.className = 't-left'; left.dataset.test = 'timer-left';
      left.textContent = T.formatRemaining(T.remainingMs(tm, now));
      const stop = document.createElement('button'); stop.type = 'button'; stop.className = 'btn btn-secondary btn-sm';
      stop.dataset.action = 'timer-stop'; stop.dataset.id = tm.id; stop.dataset.test = 'timer-stop'; stop.textContent = t('js.timer.stop');
      li.append(lab, left, stop); return li;
    });
    tray.replaceChildren(...nodes);
  }
  function updateTrayTimes() {
    const now = Date.now();
    for (const li of $$('.timer-item', tray)) {
      const tm = timers.find((x) => x.id === li.dataset.id);
      if (tm) $('.t-left', li).textContent = T.formatRemaining(T.remainingMs(tm, now));
    }
  }
  function showBanner(text) {
    $('[data-banner-text]', banner).textContent = text;
    banner.hidden = false;
  }
  function dismissBanner() {
    banner.hidden = true;
    timers = timers.filter((tm) => !tm.firedEnd);
    persist(); renderTray();
  }
  function handle(events) {
    for (const ev of events) {
      const tm = ev.timer;
      if (ev.type === 'pre') { showBanner(t('js.timer.pre', { label: tm.label })); chime(); vibrate(); }
      else if (ev.type === 'notice') { showBanner(tm.noticeText); chime(); vibrate(); }
      else if (ev.type === 'end') { showBanner(t('js.timer.end', { label: tm.label })); chime(); vibrate(); lastChime = Date.now(); }
    }
  }
  function tickNow() {
    const now = Date.now();
    const r = T.tick(timers, now);
    const changed = r.events.length > 0;
    timers = r.timers;
    if (changed) { persist(); handle(r.events); renderTray(); }
    else if (timers.length) updateTrayTimes();
    // repeat the final chime until dismissed, for up to 2 minutes
    if (!banner.hidden) {
      const fired = timers.filter((x) => x.firedEnd).map((x) => x.firedAt).filter(Boolean);
      const first = fired.length ? Math.max(...fired) : null;
      if (first != null && T.shouldRepeatChime(first, lastChime, now)) { chime(); vibrate(); lastChime = now; }
    }
  }
  function startTimer(opts) {
    unlockAudio();
    const now = Date.now();
    const tm = T.makeTimer({ id: T.newId(now), recipeId, now, ...opts });
    timers = [...timers, tm]; persist(); renderTray();
    return tm;
  }
  setInterval(tickNow, 1000);
  document.addEventListener('visibilitychange', () => { if (document.visibilityState === 'visible') tickNow(); });
  renderTray(); tickNow();

  /* ---------- ingredients tick-off ---------- */
  const tickKey = `nwr_ticks:${recipeId}`;
  const items = $$('#ingredients-panel li.ingredient');
  try { const saved = JSON.parse(sessionStorage.getItem(tickKey) || '[]'); items.forEach((li, i) => li.classList.toggle('is-ticked', saved.includes(i))); } catch { /* ignore */ }
  $('#ingredients-panel').addEventListener('click', (e) => {
    const li = e.target.closest('li.ingredient'); if (!li) return;
    li.classList.toggle('is-ticked');
    try { sessionStorage.setItem(tickKey, JSON.stringify(items.map((x, i) => (x.classList.contains('is-ticked') ? i : -1)).filter((i) => i >= 0))); } catch { /* ignore */ }
  });

  /* ---------- done ---------- */
  function openDone() { const d = $('#done-dialog'); if (d && !d.open) d.showModal(); }

  Object.assign(actions, {
    'step-next': next,
    'step-back': back,
    'stage-next'() { showStage(stageIdx + 1); },
    'stage-prev'() { showStage(stageIdx - 1); },
    'open-ingredients'() { $('#ingredients-panel').showModal(); },
    'cook-done': openDone,
    'cook-finish'() { try { sessionStorage.removeItem(tickKey); } catch { /* ignore */ } return 'native'; },
    'timer-dismiss': dismissBanner,
    'timer-stop'(el) { timers = T.removeTimer(timers, el.dataset.id); persist(); renderTray(); if (!timers.some((x) => x.firedEnd)) banner.hidden = true; },
    'step-timer-start'(el) {
      startTimer({ label: el.dataset.label, seconds: Number(el.dataset.seconds) });
    },
    'stage-timer-start'(el) {
      const st = (cfg.stages || [])[Number(el.dataset.stage)] || {};
      const seconds = st.totalseconds || Number(el.dataset.seconds) || 0;
      if (!seconds) return;
      startTimer({ label: st.label || t('js.cook.stage_n', { n: Number(el.dataset.stage) + 1 }), seconds, preAlert: st.prealert !== false && seconds >= T.PRE_ALERT_MIN_SECONDS,
        stage1Seconds: st.isgourmet ? st.stage1seconds : 0, noticeText: st.isgourmet ? st.stage2notice : '' });
    },
  });
}

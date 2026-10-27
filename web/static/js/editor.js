// Recipe editor: stage cards, oven-spec driven fields, tags, drafts, JSON submit, photos (spec 70 sections 4.5, 6).
import { actions, toast } from './app.js';
import { t } from './lib/i18n.js';
import { normalizeKeys, readJSON } from './lib/keys.js';
import * as D from './lib/drafts.js';
import * as O from './lib/oven.js';
import { createPhotoManager } from './upload.js';

const cfg = normalizeKeys(readJSON('editor-config') || {});
const spec = readJSON('oven-spec') || { modes: [] };
const MAX_STAGES = cfg.maxstages || 10;

let form = null;
let photos = null;
let baseline = null;       // fields as rendered by the server for this page load
let submitting = false;
let draftTimer = null;
let langTouched = false;
let lastSubmit = null;

const $ = (sel, root = document) => root.querySelector(sel);
const $$ = (sel, root = document) => [...root.querySelectorAll(sel)];

const draftKey = () => cfg.draftkey || D.draftKey(cfg.userid || document.body.dataset.userId || '', D.targetFor(cfg.mode, cfg.recipeid));
const fieldsNow = () => D.collectFields(form);
const isDirty = () => baseline && !D.fieldsEqual(fieldsNow(), baseline);

/* ---------- stages ---------- */
const cards = () => $$('[data-stage-card]', form);

function renumber() {
  cards().forEach((card, i) => {
    const old = card.dataset.idx;
    if (old !== String(i)) {
      const re = new RegExp(`^stage\\[${old}\\]`);
      const idRe = new RegExp(`stage-${old}-`, 'g');
      for (const el of [card, ...$$('*', card)]) {
        for (const attr of ['name', 'id', 'for', 'aria-labelledby', 'aria-describedby']) {
          const v = el.getAttribute(attr);
          if (!v) continue;
          el.setAttribute(attr, v.replace(re, `stage[${i}]`).replace(idRe, `stage-${i}-`));
        }
      }
      card.dataset.idx = String(i);
    }
    const num = $('[data-stage-num]', card); if (num) num.textContent = String(i + 1);
  });
  const addBtn = $('[data-action="add-stage"]', form);
  if (addBtn) addBtn.disabled = cards().length >= MAX_STAGES;
}

function fmtTime(sec) {
  const h = O.humanizeSeconds(sec);
  return `${h.value} ${t(`js.unit.${h.unit}`)}`;
}
function rangeText(field, r) {
  return field.startsWith('temperature') ? `${r.min}–${r.max} °C` : `${fmtTime(r.min)}–${fmtTime(r.max)}`;
}

function cardValue(card, field) {
  const el = $(`[name$=".${field}"]`, card);
  return el ? el.value : '';
}

function refreshWarnings(card) {
  const mode = cardValue(card, 'mode'); const preset = cardValue(card, 'pizza_preset');
  for (const field of ['temperature_c', 'temperature2_c', 'duration_seconds', 'duration2_seconds']) {
    const wrap = $(`[data-field="${field}"]`, card);
    if (!wrap) continue;
    let live = $('[data-live-warn]', wrap);
    const w = wrap.hidden ? null : O.warningFor(spec, mode, field, cardValue(card, field), preset);
    if (!w) { live?.remove(); continue; }
    if (!live) { live = document.createElement('p'); live.className = 'field-warn'; live.dataset.liveWarn = ''; live.setAttribute('role', 'status'); wrap.append(live); }
    const key = field.startsWith('temperature') ? 'js.warn.unusual_temp' : 'js.warn.unusual_time';
    live.textContent = `⚠️ ${t(key, { mode: t(`oven.mode.${mode}`), range: rangeText(field, w) })}`;
  }
}

function updateStage(card, { modeChanged = false } = {}) {
  const sel = (n) => $(`[name$=".${n}"]`, card);
  const mode = sel('mode').value; const preset = sel('pizza_preset')?.value || '';
  const wf = sel('woodfire');
  const fs = O.fieldState(spec, mode, preset, !!(wf && wf.checked));
  if (fs.woodfireForced && wf) wf.checked = true;
  if (!fs.visible.has('woodfire') && wf) wf.checked = false;
  // recompute after woodfire adjustments
  const fs2 = O.fieldState(spec, mode, preset, !!(wf && wf.checked));
  for (const wrap of $$('[data-field]', card)) {
    const f = wrap.dataset.field;
    const show = mode ? fs2.visible.has(f) || f === 'mode' : f === 'mode';
    wrap.hidden = !show;
    for (const input of $$('input,select,textarea', wrap)) {
      if (f === 'mode') { input.disabled = false; continue; }
      input.disabled = !show || (f === 'woodfire' && fs2.woodfireLocked);
    }
  }
  for (const g of $$('[data-field-group]', card)) g.hidden = $$('[data-field]', g).every((w) => w.hidden);
  for (const h of $$('[data-only-mode]', card)) h.hidden = h.dataset.onlyMode !== mode;
  // seconds input is only meaningful for pizza
  const secs = $('[data-secs]', card);
  if (secs) secs.hidden = !(mode === 'pizza' || Number(cardValue(card, 'duration_seconds')) % 60 !== 0);
  // phase labels for gourmet roast
  $$('[data-phase-label]', card).forEach((el) => {
    const base = el.dataset.base || (el.dataset.base = el.textContent);
    el.textContent = fs2.mode && fs2.mode.duration2 ? `${t('js.phase1')}: ${base}` : base;
  });
  // hints
  for (const field of ['temperature_c', 'temperature2_c', 'duration_seconds', 'duration2_seconds']) {
    const hint = $(`[data-hint="${field}"]`, card);
    if (!hint) continue;
    const r = O.recommended(spec, mode, field, preset);
    hint.textContent = r ? t('js.hint.recommended', { range: rangeText(field, r) }) : '';
    hint.hidden = !r;
  }
  const wh = $('[data-hint="woodfire"]', card);
  if (wh) { wh.textContent = fs2.woodfireForced ? t('js.hint.smoker_woodfire') : ''; wh.hidden = !fs2.woodfireForced; }
  if (modeChanged && mode && !card.dataset.accTouched) {
    const def = O.defaultAccessory(spec, mode);
    $$('input[name$=".accessories"]', card).forEach((c) => { c.checked = c.value === def; });
  }
  if (modeChanged && fs2.durationOptional) { /* optional duration: nothing to enforce */ }
  refreshWarnings(card);
}

function initHms(card) {
  for (const wrap of $$('.time-field', card)) {
    const hidden = $('input[type="hidden"]', wrap);
    const inputs = $$('[data-hms]', wrap);
    const fill = () => {
      const p = O.fromSeconds(hidden.value);
      const has = hidden.value !== '';
      inputs.forEach((i) => { i.value = has ? String(p[i.dataset.hms] || 0) : ''; });
    };
    fill();
    wrap.addEventListener('input', (e) => {
      if (!e.target.matches('[data-hms]')) return;
      const vals = Object.fromEntries(inputs.map((i) => [i.dataset.hms, i.value]));
      const any = inputs.some((i) => i.value !== '');
      hidden.value = any ? String(O.toSeconds(vals.h, vals.m, vals.s)) : '';
      hidden.dispatchEvent(new Event('input', { bubbles: true }));
    });
    wrap._fill = fill;
  }
}

function initStage(card) {
  initHms(card);
  updateStage(card);
}

function addStage() {
  if (cards().length >= MAX_STAGES) return null;
  const tpl = $('#stage-template', form);
  const html = tpl.innerHTML.replace(/__N__/g, String(cards().length));
  const holder = document.createElement('div');
  holder.innerHTML = html;
  const card = holder.firstElementChild;
  $('#stage-list', form).append(card);
  initStage(card); renumber();
  return card;
}

/* ---------- actions ---------- */
Object.assign(actions, {
  'add-stage'() { const c = addStage(); c?.querySelector('select')?.focus(); schedule(); },
  'remove-stage'(el) { el.closest('[data-stage-card]').remove(); renumber(); schedule(); },
  'stage-up'(el) { const c = el.closest('[data-stage-card]'); c.previousElementSibling?.before(c); renumber(); schedule(); el.focus(); },
  'stage-down'(el) { const c = el.closest('[data-stage-card]'); c.nextElementSibling?.after(c); renumber(); schedule(); el.focus(); },
  'photo-cover'(el) { photos.act('photo-cover', el.closest('.photo-tile')); },
  'photo-earlier'(el) { photos.act('photo-earlier', el.closest('.photo-tile')); },
  'photo-later'(el) { photos.act('photo-later', el.closest('.photo-tile')); },
  'photo-delete'(el) { photos.act('photo-delete', el.closest('.photo-tile')); },
  'draft-continue'() { const d = D.loadDraft(localStorage, draftKey()); if (d) applyDraft(d.fields); hideBanner(); },
  'draft-discard'() { D.clearDraft(localStorage, draftKey()); hideBanner(); },
  'save-retry'() { submit(); },
});

/* ---------- categories & tags ---------- */
function initCategories() {
  const box = $('#category-chips', form); if (!box) return;
  const max = Number(box.dataset.max || 5);
  box.addEventListener('change', (e) => {
    const checked = $$('input:checked', box);
    const note = $('#category-limit', form);
    if (checked.length > max) { e.target.checked = false; if (note) note.hidden = false; } else if (note) note.hidden = true;
  });
}

function initTags() {
  const hidden = $('#tags-value', form); const input = $('#tag-input', form);
  const chips = $('#tag-chips', form); const list = $('#tag-suggest', form);
  if (!hidden || !input) return;
  let tags = hidden.value.split(',').map((s) => s.trim()).filter(Boolean);
  let items = []; let active = -1; let timer = null; let seq = 0;
  const sync = () => { hidden.value = tags.join(','); hidden.dispatchEvent(new Event('input', { bubbles: true })); };
  const render = () => {
    chips.replaceChildren(...tags.map((tag) => {
      const li = document.createElement('li'); li.className = 'chip chip-active'; li.dataset.tag = tag;
      const span = document.createElement('span'); span.dir = 'auto'; span.textContent = tag;
      const b = document.createElement('button'); b.type = 'button'; b.className = 'chip-del'; b.setAttribute('aria-label', t('js.tag.remove', { name: tag }));
      b.innerHTML = '<svg class="ic" aria-hidden="true"><use href="#i-x"></use></svg>';
      b.addEventListener('click', () => { tags = tags.filter((x) => x !== tag); render(); sync(); input.focus(); });
      li.append(span, b); return li;
    }));
  };
  const add = (raw) => {
    const v = raw.trim().replace(/^#/, '');
    if (v && !tags.some((x) => x.toLowerCase() === v.toLowerCase())) { tags.push(v); render(); sync(); }
    input.value = ''; closeList();
  };
  const closeList = () => { list.hidden = true; list.replaceChildren(); items = []; active = -1; input.removeAttribute('aria-activedescendant'); };
  const choose = (it) => {
    if (it.type === 'category') {
      const cb = $(`#category-chips input[value="${CSS.escape(it.slug)}"]`, form);
      if (cb && !cb.checked) { cb.checked = true; cb.dispatchEvent(new Event('change', { bubbles: true })); }
      input.value = ''; closeList();
    } else add(it.name);
  };
  const paint = () => {
    list.replaceChildren(...items.map((it, i) => {
      const li = document.createElement('li'); li.role = 'option'; li.id = `sg-${i}`; li.dir = 'auto';
      li.setAttribute('aria-selected', String(i === active));
      li.textContent = `${it.type === 'category' ? '📁 ' : '#'}${it.name}`;
      li.addEventListener('mousedown', (e) => { e.preventDefault(); choose(it); });
      return li;
    }));
    list.hidden = !items.length;
    if (active >= 0) input.setAttribute('aria-activedescendant', `sg-${active}`);
  };
  input.addEventListener('input', () => {
    clearTimeout(timer);
    const q = input.value.trim();
    if (input.value.includes(',')) { input.value.split(',').forEach((p) => p.trim() && add(p)); return; }
    if (!q || !cfg.suggesturl) { closeList(); return; }
    timer = setTimeout(async () => {
      const my = ++seq;
      try {
        const res = await fetch(`${cfg.suggesturl}${cfg.suggesturl.includes('?') ? '&' : '?'}q=${encodeURIComponent(q)}`, { headers: { Accept: 'application/json' } });
        if (!res.ok || my !== seq) return;
        items = (await res.json()).slice(0, 10); active = -1; paint();
      } catch { closeList(); }
    }, 180);
  });
  input.addEventListener('keydown', (e) => {
    if (e.key === 'ArrowDown' && items.length) { e.preventDefault(); active = (active + 1) % items.length; paint(); }
    else if (e.key === 'ArrowUp' && items.length) { e.preventDefault(); active = (active - 1 + items.length) % items.length; paint(); }
    else if (e.key === 'Enter') { e.preventDefault(); if (active >= 0) choose(items[active]); else add(input.value); }
    else if (e.key === 'Escape') closeList();
    else if (e.key === 'Backspace' && !input.value && tags.length) { tags.pop(); render(); sync(); }
  });
  input.addEventListener('blur', () => { if (input.value.trim()) add(input.value); setTimeout(closeList, 120); });
  render();
  form._setTags = (v) => { tags = v.split(',').map((s) => s.trim()).filter(Boolean); render(); sync(); };
}

/* ---------- drafts ---------- */
function banner() { return $('#draft-banner'); }
function hideBanner() { const b = banner(); if (b) b.hidden = true; }
function showBanner(draft) {
  const b = banner(); if (!b) return;
  $('.draft-text', b).textContent = t('js.draft.continue');
  const w = $('.draft-warn', b);
  const stale = D.isStale(draft, cfg.version);
  w.hidden = !stale; if (stale) w.textContent = t('js.draft.changed_since');
  b.hidden = false;
}
function applyDraft(fields) {
  const byName = new Map();
  for (const [n, v] of fields) { if (!byName.has(n)) byName.set(n, []); byName.get(n).push(v); }
  let maxIdx = -1;
  for (const n of byName.keys()) { const m = /^stage\[(\d+)\]\./.exec(n); if (m) maxIdx = Math.max(maxIdx, +m[1]); }
  while (cards().length > maxIdx + 1) cards().at(-1).remove();
  while (cards().length < maxIdx + 1) addStage();
  renumber();
  for (const el of form.elements) {
    if (!el.name || el.type === 'file' || el.name === 'version') continue;
    const vals = byName.get(el.name);
    if (el.type === 'checkbox' || el.type === 'radio') el.checked = !!vals && vals.includes(el.value);
    else if (vals) el.value = vals[0]; else if (el.tagName !== 'SELECT' && el.type !== 'hidden') el.value = '';
  }
  // controls disabled by a previous mode still need their values (they were not in the draft): reapply state
  cards().forEach((c) => { $$('.time-field', c).forEach((w) => w._fill?.()); updateStage(c); });
  form._setTags?.($('#tags-value', form).value);
}
function schedule() {
  clearTimeout(draftTimer);
  draftTimer = setTimeout(saveDraftNow, 1000);
}
function saveDraftNow() {
  if (submitting || !form) return;
  const fields = fieldsNow();
  if (!isDirty() || !D.hasMeaningfulContent(fields)) return;
  D.saveDraft(localStorage, draftKey(), { baseVersion: cfg.version, fields });
}

/* ---------- submit ---------- */
function setStatus(text) { const s = $('#save-status'); if (s) s.textContent = text || ''; }

function swapForm(html) {
  const tpl = document.createElement('template'); tpl.innerHTML = html.trim();
  const fresh = tpl.content.querySelector('#recipe-form');
  if (!fresh) return false;
  // keep live photo nodes (queued blobs, listeners)
  for (const sel of ['.add-photo', '#photo-list', '#photo-queue', '#photo-msg']) {
    const oldEl = $(sel, form); const newEl = $(sel, fresh);
    if (oldEl && newEl) newEl.replaceWith(oldEl);
  }
  form.replaceWith(fresh);
  bind(fresh);
  const sum = $('#error-summary', fresh);
  if (sum && !sum.hidden) { sum.scrollIntoView({ block: 'center', behavior: 'smooth' }); sum.focus({ preventScroll: true }); }
  return true;
}

async function submit() {
  if (submitting) return;
  submitting = true;
  const btn = $('[data-test="save"]', form); if (btn) btn.disabled = true;
  setStatus(t('js.saving'));
  $('#offline-banner').hidden = true;
  $('#conflict-slot').replaceChildren();
  let res;
  try {
    res = await fetch(form.action, { method: 'POST', body: new FormData(form), headers: { Accept: 'application/json' }, credentials: 'same-origin' });
  } catch {
    submitting = false; if (btn) btn.disabled = false;
    setStatus(''); saveDraftNow();
    $('#offline-banner').hidden = false;
    lastSubmit = 'offline';
    return;
  }
  lastSubmit = null;
  const redirectedToSignin = res.redirected && /\/signin/.test(new URL(res.url).pathname);
  if (res.status === 401 || redirectedToSignin || res.headers.get('HX-Redirect')) {
    submitting = false; saveDraftNow();
    const back = location.pathname + location.search;
    location.href = cfg.signinurl || `/signin?return_to=${encodeURIComponent(back)}`;
    return;
  }
  if (res.status === 200 && (res.headers.get('Content-Type') || '').includes('json')) {
    const json = await res.json();
    let failed = 0;
    if (photos && photos.queued) {
      setStatus(t('js.photo.uploading'));
      failed = await photos.flush(cfg.uploadurl || `/recipes/${json.id}/images`);
    }
    D.clearDraft(localStorage, draftKey());
    if (failed) {
      try { sessionStorage.setItem('nwr_notice', t('js.photo.failed_after_save')); } catch { /* ignore */ }
      location.href = `/recipes/${json.id}/edit`;
    } else location.href = json.url || `/recipes/${json.id}`;
    return;
  }
  submitting = false; if (btn) btn.disabled = false; setStatus('');
  const text = await res.text();
  if (res.status === 422 && swapForm(text)) return;
  if (res.status === 409) {
    const tpl = document.createElement('template'); tpl.innerHTML = text.trim();
    const node = tpl.content.querySelector('#conflict') || tpl.content.firstElementChild;
    if (node) $('#conflict-slot').replaceChildren(node);
    saveDraftNow(); node?.scrollIntoView({ block: 'center' });
    return;
  }
  saveDraftNow();
  toast(t('js.error.generic'), 'error');
}

/* ---------- bind / init ---------- */
function bind(f) {
  form = f;
  cards().forEach(initStage);
  renumber();
  initCategories();
  initTags();
  if (!photos) {
    const input = $('#photo-input', form);
    if (input) photos = createPhotoManager({ cfg, list: $('#photo-list', form), queue: $('#photo-queue', form), msg: $('#photo-msg', form), input });
  }
  form.addEventListener('submit', (e) => { e.preventDefault(); submit(); });
  form.addEventListener('input', (e) => {
    const n = e.target.name || '';
    if (/\.accessories$/.test(n)) e.target.closest('[data-stage-card]').dataset.accTouched = '1';
    if (n === 'language') langTouched = true;
    if (n === 'title' && !langTouched) {
      const lang = $('#f-language', form);
      if (lang && (lang.value === 'und' || lang.value === '') && O.hasHebrew(e.target.value)) lang.value = 'he';
    }
    const card = e.target.closest('[data-stage-card]');
    if (card) refreshWarnings(card);
    schedule();
  });
  form.addEventListener('change', (e) => {
    const card = e.target.closest('[data-stage-card]');
    if (card) {
      const n = e.target.name || '';
      if (/\.(mode|pizza_preset|woodfire)$/.test(n)) updateStage(card, { modeChanged: /\.mode$/.test(n) });
      else refreshWarnings(card);
    }
    schedule();
  });
  form.addEventListener('keydown', (e) => {
    if (e.key === 'Enter' && e.target.matches('input:not([type="submit"]):not([type="checkbox"]):not([type="radio"])')) e.preventDefault();
  });
}

function boot() {
  const f = document.getElementById('recipe-form');
  if (!f) return;
  bind(f);
  baseline = fieldsNow();
  const draft = D.loadDraft(localStorage, draftKey());
  if (draft && !D.fieldsEqual(draft.fields, baseline) && D.hasMeaningfulContent(draft.fields)) showBanner(draft);
}

document.addEventListener('visibilitychange', () => { if (document.visibilityState === 'hidden') saveDraftNow(); });
window.addEventListener('beforeunload', (e) => {
  if (!submitting && form && isDirty()) { e.preventDefault(); e.returnValue = ''; }
});
window.addEventListener('online', () => { if (lastSubmit === 'offline') submit(); });
document.addEventListener('keydown', (e) => {
  if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === 's' && form) { e.preventDefault(); form.requestSubmit(); }
});

boot();

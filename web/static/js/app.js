// Loaded on every page. Event delegation on [data-action]; no inline handlers (CSP).
import { t } from './lib/i18n.js';
import { clearUserDrafts, pruneDrafts } from './lib/drafts.js';
import { formatQty, scaleAmount } from './lib/scale.js';

const store = {
  get(k) { try { return localStorage.getItem(k); } catch { return null; } },
  set(k, v) { try { localStorage.setItem(k, v); } catch { /* ignore */ } },
};

/* ---------- toast ---------- */
export function toast(text, kind = 'success') {
  const region = document.getElementById('flash');
  if (!region) return;
  const el = document.createElement('div');
  el.className = `flash flash-${kind}`;
  el.setAttribute('role', kind === 'error' ? 'alert' : 'status');
  el.dataset.test = 'flash';
  const span = document.createElement('span');
  span.className = 'flash-text';
  span.textContent = text;
  el.append(span);
  region.replaceChildren(el);
  clearTimeout(toast.t);
  toast.t = setTimeout(() => el.remove(), 6000);
}

/* ---------- theme ---------- */
const THEMES = ['auto', 'light', 'dark'];
function currentTheme() { const v = store.get('nwr_theme'); return THEMES.includes(v) ? v : 'auto'; }
function applyTheme(theme) {
  const root = document.documentElement;
  if (theme === 'auto') root.removeAttribute('data-theme'); else root.setAttribute('data-theme', theme);
  document.querySelectorAll('[data-theme-label]').forEach((el) => { el.textContent = t(`js.theme.${theme}`); });
}

/* ---------- dialogs ---------- */
function openDialog(id) {
  const d = document.getElementById(id);
  if (d && typeof d.showModal === 'function' && !d.open) d.showModal();
  return d;
}
document.addEventListener('click', (e) => {
  // click on the backdrop of a modal dialog closes it
  if (e.target instanceof HTMLDialogElement && e.target.open) {
    const r = e.target.getBoundingClientRect();
    const inside = e.clientX >= r.left && e.clientX <= r.right && e.clientY >= r.top && e.clientY <= r.bottom;
    if (!inside && e.target.id !== 'done-dialog') e.target.close();
  }
});

/* single-choice chips that tapping the selected one clears (optional throw-away reasons) */
document.addEventListener('click', (e) => {
  const input = e.target;
  if (!(input instanceof HTMLInputElement) || input.type !== 'radio' || !input.hasAttribute('data-clearable')) return;
  const was = input.dataset.was === '1';
  input.form?.querySelectorAll(`input[type="radio"][name="${input.name}"]`).forEach((r) => { r.dataset.was = '0'; });
  if (was) input.checked = false; else input.dataset.was = '1';
});

/* ---------- actions ---------- */
async function copyText(text) {
  try { await navigator.clipboard.writeText(text); return true; } catch { /* fall back */ }
  const ta = document.createElement('textarea');
  ta.value = text; ta.className = 'sr-only'; document.body.append(ta); ta.select();
  let ok = false; try { ok = document.execCommand('copy'); } catch { ok = false; }
  ta.remove(); return ok;
}

export const actions = {
  'open-dialog'(el) {
    const d = openDialog(el.dataset.target);
    if (d) { const af = d.querySelector('[autofocus]'); if (af) af.focus(); }
  },
  'close-dialog'(el) { el.closest('dialog')?.close(); },
  'theme-toggle'() {
    const next = THEMES[(THEMES.indexOf(currentTheme()) + 1) % THEMES.length];
    store.set('nwr_theme', next);
    applyTheme(next);
  },
  'dismiss-flash'(el) { el.closest('.flash')?.remove(); },
  async 'share-link'(el) {
    const url = el.dataset.url; const title = el.dataset.title;
    if (navigator.share) {
      try { await navigator.share({ title, url }); return; } catch (err) { if (err && err.name === 'AbortError') return; }
    }
    toast(await copyText(url) ? t('js.share.copied') : t('js.share.failed'), 'success');
  },
  async 'copy-text'(el) {
    const input = document.getElementById(el.dataset.target);
    if (!input) return;
    input.select?.();
    toast(await copyText(input.value) ? t('js.copied') : t('js.share.failed'), 'success');
  },
  'open-image'(el) {
    const d = document.getElementById('lightbox');
    if (!d) return 'native';
    const img = d.querySelector('img');
    img.src = el.dataset.src || el.getAttribute('href');
    img.alt = el.dataset.caption || '';
    openDialog('lightbox');
  },
  'servings-step'(el) { stepServings(el.closest('[data-servings]'), Number(el.dataset.delta)); },
};

document.addEventListener('click', (e) => {
  const el = e.target.closest('[data-action]');
  if (!el || el.tagName === 'FORM') return;
  const fn = actions[el.dataset.action];
  if (fn) { if (fn(el, e) !== 'native') e.preventDefault(); }
});

/* sign-out: wipe drafts and cached pages of this device, then let the form submit */
document.addEventListener('submit', (e) => {
  const f = e.target;
  if (f instanceof HTMLFormElement && f.dataset.action === 'sign-out') {
    const uid = document.body.dataset.userId;
    try { if (uid) clearUserDrafts(localStorage, uid); } catch { /* ignore */ }
    try { sessionStorage.clear(); } catch { /* ignore */ }
    navigator.serviceWorker?.controller?.postMessage({ type: 'clear-cache' });
  }
}, true);

/* ---------- photo resize before cook-log submit (plain and htmx) ---------- */
document.addEventListener('submit', async (e) => {
  const f = e.target;
  if (!(f instanceof HTMLFormElement) || !f.hasAttribute('data-resize-photo') || f.dataset.resized) return;
  const input = f.querySelector('input[type="file"]');
  if (!input || !input.files || !input.files.length) return;
  e.preventDefault(); e.stopImmediatePropagation();
  const { resizeImage } = await import('./upload.js');
  try {
    const blob = await resizeImage(input.files[0]);
    const dt = new DataTransfer();
    dt.items.add(new File([blob], 'photo.jpg', { type: 'image/jpeg' }));
    input.files = dt.files;
  } catch (err) {
    toast(t(err && err.code === 'heic' ? 'js.photo.heic' : 'js.photo.failed'), 'error');
    return;
  }
  f.dataset.resized = '1';
  f.requestSubmit();
}, true);

/* ---------- servings scaling (recipe page; cook mode re-uses the same markup) ---------- */
function stepServings(box, delta) {
  if (!box) return;
  const orig = Number(box.dataset.servings);
  const cur = Number(box.dataset.current || orig);
  const next = Math.min(99, Math.max(1, cur + delta));
  box.dataset.current = String(next);
  const out = box.querySelector('[data-servings-value]');
  if (out) out.textContent = String(next);
  document.querySelectorAll('ul.ingredients').forEach((ul) => {
    ul.querySelectorAll('li.ingredient[data-amount]').forEach((li) => {
      const base = Number(li.dataset.amount);
      if (!(base > 0)) return;
      const q = formatQty(scaleAmount(base, orig, next), li.dataset.unit || '');
      li.querySelector('.ing-qty bdi').textContent = q.text;
      const u = li.querySelector('.ing-unit');
      if (u) u.textContent = t(`js.unit.${q.unit}`);
    });
  });
}

/* ---------- visibility message after htmx swap ---------- */
let visibilityToggled = false;
document.addEventListener('htmx:beforeRequest', (e) => {
  visibilityToggled = !!(e.detail.elt && e.detail.elt.closest && e.detail.elt.closest('#visibility-control'));
});
document.addEventListener('htmx:afterSettle', () => {
  if (!visibilityToggled) return;
  visibilityToggled = false;
  const box = document.getElementById('visibility-control');
  if (!box) return;
  const on = box.querySelector('[role="switch"]')?.getAttribute('aria-checked') === 'true';
  const msg = box.querySelector(`[data-msg="${on ? 'on' : 'off'}"]`);
  if (msg) msg.hidden = false;
  box.querySelector('[role="switch"]')?.focus();
});
document.addEventListener('htmx:responseError', (e) => {
  if (e.detail?.xhr?.status === 401) return;
  toast(t('js.error.generic'), 'error');
});
document.addEventListener('htmx:sendError', () => toast(t('js.offline'), 'error'));

/* ---------- keyboard shortcuts ---------- */
document.addEventListener('keydown', (e) => {
  const tag = (e.target.tagName || '').toLowerCase();
  const typing = tag === 'input' || tag === 'textarea' || tag === 'select' || e.target.isContentEditable;
  if (e.key === '/' && !typing && !e.ctrlKey && !e.metaKey && !e.altKey) {
    const s = document.querySelector('input[type="search"]');
    if (s) { e.preventDefault(); s.focus(); }
  }
});

/* ---------- boot ---------- */
applyTheme(currentTheme());
try { pruneDrafts(localStorage); } catch { /* ignore */ }

if ('serviceWorker' in navigator) {
  addEventListener('load', () => { navigator.serviceWorker.register('/sw.js', { scope: '/' }).catch(() => {}); });
}
// Cache this page for offline reading when it is a recipe or cook page (the SW also does this on fetch).
try {
  const n = sessionStorage.getItem('nwr_notice');
  if (n) { sessionStorage.removeItem('nwr_notice'); toast(n, 'warning'); }
} catch { /* ignore */ }

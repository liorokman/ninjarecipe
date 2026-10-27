// Client-side photo handling (spec 70 section 6.3): resize to JPEG <= 2048px, sequential XHR upload with
// progress, an in-memory queue for new recipes, retry, and reorder / cover / delete.
import { t } from './lib/i18n.js';

export const MAX_EDGE = 2048;
export const QUALITY = 0.85;

export async function resizeImage(file, maxEdge = MAX_EDGE, quality = QUALITY) {
  let bmp;
  try { bmp = await createImageBitmap(file, { imageOrientation: 'from-image' }); }
  catch { const e = new Error('decode'); e.code = 'heic'; throw e; }
  const scale = Math.min(1, maxEdge / Math.max(bmp.width, bmp.height));
  const w = Math.max(1, Math.round(bmp.width * scale));
  const h = Math.max(1, Math.round(bmp.height * scale));
  const canvas = document.createElement('canvas');
  canvas.width = w; canvas.height = h;
  const ctx = canvas.getContext('2d');
  ctx.fillStyle = '#fff'; ctx.fillRect(0, 0, w, h);
  ctx.drawImage(bmp, 0, 0, w, h);
  bmp.close?.();
  const blob = await new Promise((res) => canvas.toBlob(res, 'image/jpeg', quality));
  if (!blob) { const e = new Error('encode'); e.code = 'failed'; throw e; }
  return blob;
}

export function uploadImage(url, blob, onProgress) {
  return new Promise((resolve) => {
    const xhr = new XMLHttpRequest();
    xhr.open('POST', url);
    xhr.setRequestHeader('Accept', 'text/html');
    xhr.setRequestHeader('X-Requested-With', 'XMLHttpRequest');
    xhr.upload.onprogress = (e) => { if (e.lengthComputable && onProgress) onProgress(Math.round((e.loaded / e.total) * 100)); };
    xhr.onload = () => resolve({ status: xhr.status, html: xhr.responseText, imageId: xhr.getResponseHeader('X-Image-Id') });
    xhr.onerror = () => resolve({ status: 0, html: '', imageId: null });
    const fd = new FormData();
    fd.append('image', blob, 'photo.jpg');
    xhr.send(fd);
  });
}

function textOf(html) {
  const doc = new DOMParser().parseFromString(html || '', 'text/html');
  return (doc.body.textContent || '').trim();
}

function htmlToNode(html) {
  const tpl = document.createElement('template');
  tpl.innerHTML = html.trim();
  return tpl.content.firstElementChild;
}

export function createPhotoManager({ cfg, list, queue, msg, input }) {
  const pending = []; // new-recipe queue: {tile, blob}
  const orderURL = cfg.orderurl || '';
  const baseURL = orderURL ? orderURL.replace(/\/order$/, '') : '';
  const saved = !!cfg.uploadurl;

  const say = (text, kind = 'info') => { msg.textContent = text; msg.className = kind === 'error' ? 'field-error' : 'hint'; };

  function makeTile(blob) {
    const li = document.createElement('li');
    li.className = 'photo-tile is-uploading';
    li.dataset.test = 'photo-tile';
    const img = document.createElement('img');
    img.alt = ''; img.width = 160; img.height = 120;
    img.src = URL.createObjectURL(blob);
    const prog = document.createElement('span'); prog.className = 'progress';
    prog.setAttribute('role', 'progressbar'); prog.setAttribute('aria-valuemin', '0'); prog.setAttribute('aria-valuemax', '100');
    li.append(img, prog);
    return li;
  }
  function setProgress(li, p) { li.style.setProperty('--p', String(p)); li.querySelector('.progress')?.setAttribute('aria-valuenow', String(p)); }

  function addActions(li) {
    const bar = document.createElement('div'); bar.className = 'photo-actions';
    const mk = (action, labelKey, icon) => {
      const b = document.createElement('button'); b.type = 'button'; b.className = 'icon-btn'; b.dataset.action = action;
      b.setAttribute('aria-label', t(labelKey));
      b.innerHTML = `<svg class="ic${/earlier|later/.test(action) ? ' icon-dir' : ''}" aria-hidden="true"><use href="#i-${icon}"></use></svg>`;
      return b;
    };
    bar.append(mk('photo-cover', 'js.photo.make_cover', 'star'), mk('photo-earlier', 'js.photo.earlier', 'chevron'),
      mk('photo-later', 'js.photo.later', 'chevron'), mk('photo-delete', 'js.photo.delete', 'x'));
    li.append(bar);
  }

  function fail(li, text, retry) {
    li.classList.remove('is-uploading'); li.classList.add('is-failed');
    li.querySelector('.photo-error')?.remove();
    const box = document.createElement('div'); box.className = 'photo-error'; box.setAttribute('role', 'alert');
    const p = document.createElement('span'); p.textContent = text;
    const b = document.createElement('button'); b.type = 'button'; b.className = 'btn btn-sm btn-secondary'; b.textContent = t('js.photo.retry');
    b.dataset.test = 'photo-retry';
    b.addEventListener('click', () => { box.remove(); li.classList.remove('is-failed'); li.classList.add('is-uploading'); retry(); });
    box.append(p, b); li.append(box);
  }

  async function uploadTile(li, blob, url) {
    li.classList.add('is-uploading'); setProgress(li, 0);
    const r = await uploadImage(url, blob, (p) => setProgress(li, p));
    if (r.status === 201) {
      const node = htmlToNode(r.html);
      if (node) { li.replaceWith(node); return { ok: true, node }; }
      li.remove(); return { ok: true };
    }
    const text = r.status === 0 ? t('js.photo.failed') : (textOf(r.html) || t('js.photo.failed'));
    fail(li, text, () => uploadTile(li, blob, url));
    return { ok: false };
  }

  async function handleFiles(files) {
    say('');
    for (const file of files) {
      if (!/^image\/(jpeg|png|webp)$/.test(file.type) && !/\.(jpe?g|png|webp)$/i.test(file.name)) {
        say(t('js.photo.heic'), 'error'); continue;
      }
      let blob;
      try { blob = await resizeImage(file); }
      catch (err) { say(t(err.code === 'heic' ? 'js.photo.heic' : 'js.photo.failed'), 'error'); continue; }
      if (saved) {
        const li = makeTile(blob); list.append(li);
        await uploadTile(li, blob, cfg.uploadurl);
        refreshCover();
      } else {
        const li = makeTile(blob); li.classList.remove('is-uploading'); addActions(li);
        li.dataset.queued = '1'; queue.append(li); pending.push({ li, blob });
        refreshCover();
      }
    }
    input.value = '';
  }

  function refreshCover() {
    const all = [...list.children, ...queue.children].filter((l) => l.matches('.photo-tile'));
    all.forEach((li, i) => {
      let b = li.querySelector('.cover-badge');
      if (i === 0) {
        if (!b) { b = document.createElement('span'); b.className = 'cover-badge'; b.textContent = t('js.photo.cover'); li.prepend(b); }
        b.hidden = false;
      } else if (b) b.hidden = true;
    });
  }

  async function postOrder() {
    if (!saved || !orderURL) return true;
    const body = new URLSearchParams();
    list.querySelectorAll('[data-image-id]').forEach((li) => body.append('ids', li.dataset.imageId));
    try {
      const res = await fetch(orderURL, { method: 'POST', body, headers: { Accept: 'application/json', 'X-Requested-With': 'XMLHttpRequest' } });
      if (!res.ok) throw new Error(String(res.status));
      return true;
    } catch { say(t('js.photo.order_failed'), 'error'); return false; }
  }

  async function act(action, li) {
    const parent = li.parentElement;
    if (action === 'photo-delete') {
      if (li.dataset.queued) {
        const i = pending.findIndex((p) => p.li === li); if (i >= 0) pending.splice(i, 1);
        li.remove(); refreshCover(); return;
      }
      try {
        const res = await fetch(`${baseURL}/${li.dataset.imageId}/delete`, { method: 'POST', headers: { Accept: 'application/json', 'X-Requested-With': 'XMLHttpRequest' } });
        if (!(res.ok || res.status === 204)) throw new Error(String(res.status));
        li.remove(); refreshCover();
      } catch { say(t('js.photo.delete_failed'), 'error'); }
      return;
    }
    if (action === 'photo-cover') parent.prepend(li);
    if (action === 'photo-earlier' && li.previousElementSibling) li.previousElementSibling.before(li);
    if (action === 'photo-later' && li.nextElementSibling) li.nextElementSibling.after(li);
    refreshCover();
    if (!li.dataset.queued) await postOrder();
  }

  // Upload photos that were queued while creating the recipe. Returns the number that failed.
  async function flush(url) {
    let failed = 0;
    for (const p of [...pending].filter((x) => x.li.isConnected)) {
      const r = await uploadTile(p.li, p.blob, url);
      if (r.ok) pending.splice(pending.indexOf(p), 1); else failed++;
    }
    return failed;
  }

  input.addEventListener('change', () => handleFiles([...input.files]));
  refreshCover();
  return { act, flush, get queued() { return pending.length; }, say };
}

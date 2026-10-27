// Editor draft storage (spec 70 section 6.4). Pure functions over a Storage-like object.
export const PREFIX = 'nwr_draft:';
export const MAX_AGE_MS = 30 * 24 * 60 * 60 * 1000;

export function draftKey(userId, target) { return `${PREFIX}${userId}:${target}`; }

// target: 'new' | recipe id | 'inbox:<id>'
export function targetFor(mode, recipeId) {
  if (mode === 'new') return 'new';
  if (mode === 'inbox') return `inbox:${recipeId}`;
  return recipeId;
}

// Collects form fields as [[name, value], ...] excluding files, unnamed and disabled controls.
export function collectFields(form) {
  const out = [];
  for (const el of form.elements) {
    if (!el.name || el.disabled || el.type === 'file' || el.type === 'submit' || el.type === 'button') continue;
    if ((el.type === 'checkbox' || el.type === 'radio') && !el.checked) continue;
    out.push([el.name, el.value]);
  }
  return out;
}

export function saveDraft(storage, key, { baseVersion, fields }, now = Date.now()) {
  try { storage.setItem(key, JSON.stringify({ savedAt: now, baseVersion: baseVersion ?? 0, fields })); return true; }
  catch { return false; }
}

export function loadDraft(storage, key) {
  try {
    const raw = storage.getItem(key);
    if (!raw) return null;
    const d = JSON.parse(raw);
    if (!d || !Array.isArray(d.fields)) return null;
    return d;
  } catch { return null; }
}

export function clearDraft(storage, key) { try { storage.removeItem(key); } catch { /* ignore */ } }

export function listKeys(storage) {
  const keys = [];
  try { for (let i = 0; i < storage.length; i++) keys.push(storage.key(i)); } catch { /* ignore */ }
  return keys;
}

export function pruneDrafts(storage, now = Date.now()) {
  let removed = 0;
  for (const k of listKeys(storage)) {
    if (!k || !k.startsWith(PREFIX)) continue;
    const d = loadDraft(storage, k);
    if (!d || now - d.savedAt > MAX_AGE_MS) { clearDraft(storage, k); removed++; }
  }
  return removed;
}

export function clearUserDrafts(storage, userId) {
  const p = `${PREFIX}${userId}:`;
  let removed = 0;
  for (const k of listKeys(storage)) if (k && k.startsWith(p)) { clearDraft(storage, k); removed++; }
  return removed;
}

// Is the draft older than the server copy? (baseVersion < current version)
export function isStale(draft, currentVersion) { return !!currentVersion && draft.baseVersion < currentVersion; }

export function hasMeaningfulContent(fields) {
  return fields.some(([n, v]) => ['title', 'ingredients', 'steps', 'description', 'notes'].includes(n) && String(v).trim() !== '');
}

export function fieldsEqual(a, b) { return JSON.stringify(a) === JSON.stringify(b); }

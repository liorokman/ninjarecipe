// Normalises JSON config keys so templates may use Go field names (RecipeID) or snake_case (recipe_id).
export function normalizeKeys(v) {
  if (Array.isArray(v)) return v.map(normalizeKeys);
  if (v && typeof v === 'object') {
    const out = {};
    for (const [k, val] of Object.entries(v)) out[k.replace(/[_-]/g, '').toLowerCase()] = normalizeKeys(val);
    return out;
  }
  return v;
}

export function readJSON(id, doc = document) {
  const el = doc.getElementById(id);
  if (!el) return null;
  try { return JSON.parse(el.textContent || 'null'); } catch { return null; }
}

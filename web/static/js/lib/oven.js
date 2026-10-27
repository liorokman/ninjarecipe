// Client-side mirror of the oven spec rules (docs/specs/25-oven-settings.md), driven by the embedded #oven-spec JSON.
export const STAGE_FIELDS = ['pizza_preset', 'temperature_c', 'duration_seconds', 'temperature2_c', 'duration2_seconds',
  'woodfire', 'pellet_blend', 'pellet_note', 'preheat_minutes', 'accessories', 'target_core_temp_c', 'notes'];

export function findMode(spec, key) { return (spec && spec.modes || []).find((m) => m.key === key) || null; }

// Which fields are shown (and therefore submitted) for the chosen mode/preset/woodfire state.
export function fieldState(spec, modeKey, preset, woodfireOn) {
  const m = findMode(spec, modeKey);
  const none = { mode: null, visible: new Set(), woodfireForced: false, woodfireLocked: false, broilHint: false, durationOptional: false };
  if (!m) return none;
  const v = new Set(['accessories', 'notes', 'target_core_temp_c']);
  const tempKind = m.temperature && m.temperature.kind;
  if (m.key === 'pizza') v.add('pizza_preset');
  if (tempKind === 'required' || (tempKind === 'by_preset' && preset === 'custom')) v.add('temperature_c');
  v.add('duration_seconds');
  if (m.temperature2) v.add('temperature2_c');
  if (m.duration2) v.add('duration2_seconds');
  const woodfireAllowed = m.woodfire !== 'forbidden';
  const forced = m.woodfire === 'forced';
  if (woodfireAllowed) v.add('woodfire');
  if (woodfireAllowed && (forced || woodfireOn)) { v.add('pellet_blend'); v.add('pellet_note'); }
  if (m.preheat) v.add('preheat_minutes');
  return {
    mode: m, visible: v, woodfireForced: forced, woodfireLocked: forced,
    broilHint: m.key === 'broil', durationOptional: !!(m.duration && m.duration.kind === 'optional'),
  };
}

export function defaultAccessory(spec, modeKey) {
  const m = findMode(spec, modeKey);
  return (m && m.default_accessory) || (modeKey === 'pizza' ? 'pizza_stone' : 'pro_heat_pan');
}

// field -> spec block: temperature_c->temperature, duration_seconds->duration, ...
const BLOCK = { temperature_c: 'temperature', temperature2_c: 'temperature2', duration_seconds: 'duration', duration2_seconds: 'duration2' };
export function recommended(spec, modeKey, field, preset) {
  const m = findMode(spec, modeKey);
  const b = m && m[BLOCK[field]];
  if (!b || !b.recommended) return null;
  if (field === 'temperature_c' && b.kind === 'by_preset' && preset !== 'custom') return null;
  return { min: b.recommended[0], max: b.recommended[1], unit: field.includes('temperature') ? 'c' : 's' };
}

// Non-blocking "unusual" warning; null when fine or the value is empty.
export function warningFor(spec, modeKey, field, value, preset) {
  if (value === '' || value == null || Number.isNaN(Number(value))) return null;
  const r = recommended(spec, modeKey, field, preset);
  if (!r) return null;
  const n = Number(value);
  return n < r.min || n > r.max ? r : null;
}

export function toSeconds(h, m, s = 0) { return (Math.max(0, +h || 0) * 3600) + (Math.max(0, +m || 0) * 60) + Math.max(0, +s || 0); }
export function fromSeconds(total) {
  const t = Math.max(0, Math.floor(Number(total) || 0));
  return { h: Math.floor(t / 3600), m: Math.floor((t % 3600) / 60), s: t % 60 };
}

// Short duration for hints: 90 -> "1:30" style is not used; 60 -> "1 min" built by the caller. Returns {value, unit:'s'|'min'|'h'}.
export function humanizeSeconds(sec) {
  if (sec % 3600 === 0) return { value: sec / 3600, unit: 'h' };
  if (sec % 60 === 0) return { value: sec / 60, unit: 'min' };
  return { value: sec, unit: 's' };
}

export function hasHebrew(text) { return /[֐-׿]/.test(text || ''); }

export function renumberName(name, idx) { return name.replace(/^stage\[[^\]]*\]/, `stage[${idx}]`); }

import { readJSON } from './keys.js';

let table = null;
export function setTable(t) { table = t; }
export function t(key, params) {
  if (!table) table = readJSON('client-i18n') || {};
  let s = table[key];
  if (typeof s !== 'string') return key;
  if (params) s = s.replace(/\{(\w+)\}/g, (m, k) => (k in params ? String(params[k]) : m));
  return s;
}

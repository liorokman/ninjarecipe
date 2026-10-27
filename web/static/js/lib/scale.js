// Portion scaling (spec 70 section 5.4): multiply data-amount and format like fmtQty.
const FRACTIONS = [[0.125, '⅛'], [0.25, '¼'], [1 / 3, '⅓'], [0.375, '⅜'], [0.5, '½'], [0.625, '⅝'], [2 / 3, '⅔'], [0.75, '¾'], [0.875, '⅞']];
const FRACTION_UNITS = new Set(['tsp', 'tbsp', 'pcs', 'pinch']);

function trim(n) { return String(Math.round(n * 100) / 100); }

export function formatNumber(amount, unit) {
  if (FRACTION_UNITS.has(unit || '') && amount < 100) {
    const whole = Math.floor(amount + 1e-9);
    const frac = amount - whole;
    if (frac < 0.02) return String(whole);
    if (frac > 0.98) return String(whole + 1);
    for (const [v, ch] of FRACTIONS) if (Math.abs(frac - v) < 0.02) return whole ? `${whole} ${ch}` : ch;
  }
  return trim(amount);
}

// Returns {text, unit} where unit may switch g->kg / ml->l at >= 1000. unit is the key ('g','kg','ml','l',...).
export function formatQty(amount, unit) {
  let a = amount; let u = unit || '';
  if (u === 'g' && a >= 1000) { a /= 1000; u = 'kg'; }
  else if (u === 'ml' && a >= 1000) { a /= 1000; u = 'l'; }
  return { text: formatNumber(a, u), unit: u };
}

export function scaleAmount(amount, original, wanted) {
  if (!original || original <= 0) return amount;
  return amount * (wanted / original);
}

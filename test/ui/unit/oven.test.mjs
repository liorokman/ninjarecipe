import test from 'node:test';
import assert from 'node:assert/strict';
import * as O from '../../../web/static/js/lib/oven.js';

const rng = (a, b) => [a, b];
const mode = (key, o) => ({ key, dial: key.toUpperCase(), preheat: true, frame_level: 'bottom', woodfire: 'allowed', default_accessory: 'pro_heat_pan',
  temperature: { kind: 'required', accepted: rng(40, 370), recommended: rng(150, 370) },
  duration: { kind: 'required', accepted: rng(60, 14400), recommended: rng(300, 7200) }, temperature2: null, duration2: null, ...o });
const spec = { modes: [
  mode('pizza', { default_accessory: 'pizza_stone', temperature: { kind: 'by_preset', accepted: rng(40, 370), recommended: rng(175, 370) } }),
  mode('gourmet_roast', { temperature: { kind: 'required', accepted: rng(40, 370), recommended: rng(200, 370) },
    temperature2: { kind: 'required', accepted: rng(40, 370), recommended: rng(95, 250) }, duration2: { kind: 'required', accepted: rng(60, 21600), recommended: rng(600, 14400) } }),
  mode('max_roast', { temperature: { kind: 'required', accepted: rng(40, 370), recommended: rng(230, 370) } }),
  mode('smoker', { preheat: false, woodfire: 'forced', temperature: { kind: 'required', accepted: rng(40, 370), recommended: rng(60, 190) } }),
  mode('broil', { preheat: false, woodfire: 'forbidden', frame_level: 'top' }),
  mode('keep_warm', { preheat: false, woodfire: 'forbidden', temperature: { kind: 'forbidden', accepted: null, recommended: null }, duration: { kind: 'optional', accepted: rng(0, 43200), recommended: rng(0, 7200) } }),
] };

test('pizza preset hides temperature unless custom', () => {
  assert.equal(O.fieldState(spec, 'pizza', 'artisan', false).visible.has('temperature_c'), false);
  assert.equal(O.fieldState(spec, 'pizza', 'custom', false).visible.has('temperature_c'), true);
  assert.equal(O.fieldState(spec, 'pizza', 'artisan', false).visible.has('pizza_preset'), true);
  assert.equal(O.fieldState(spec, 'max_roast', '', false).visible.has('pizza_preset'), false);
});

test('gourmet roast shows two phases only for gourmet', () => {
  const g = O.fieldState(spec, 'gourmet_roast', '', false).visible;
  assert.ok(g.has('temperature2_c') && g.has('duration2_seconds'));
  const m = O.fieldState(spec, 'max_roast', '', false).visible;
  assert.ok(!m.has('temperature2_c') && !m.has('duration2_seconds'));
});

test('smoker forces woodfire; broil and keep warm forbid it', () => {
  const s = O.fieldState(spec, 'smoker', '', false);
  assert.ok(s.woodfireForced && s.visible.has('woodfire') && s.visible.has('pellet_blend'));
  assert.ok(!O.fieldState(spec, 'smoker', '', false).visible.has('preheat_minutes'));
  for (const k of ['broil', 'keep_warm']) {
    const f = O.fieldState(spec, k, '', true);
    assert.ok(!f.visible.has('woodfire') && !f.visible.has('pellet_blend') && !f.visible.has('pellet_note'));
  }
  assert.ok(O.fieldState(spec, 'broil', '', false).broilHint);
});

test('pellets follow woodfire switch; preheat follows mode', () => {
  assert.ok(!O.fieldState(spec, 'max_roast', '', false).visible.has('pellet_blend'));
  assert.ok(O.fieldState(spec, 'max_roast', '', true).visible.has('pellet_blend'));
  assert.ok(O.fieldState(spec, 'max_roast', '', false).visible.has('preheat_minutes'));
});

test('keep warm: no temperature, optional duration', () => {
  const f = O.fieldState(spec, 'keep_warm', '', false);
  assert.ok(!f.visible.has('temperature_c')); assert.ok(f.durationOptional);
});

test('unknown mode shows nothing', () => { assert.equal(O.fieldState(spec, '', '', false).visible.size, 0); });

test('default accessory', () => {
  assert.equal(O.defaultAccessory(spec, 'pizza'), 'pizza_stone');
  assert.equal(O.defaultAccessory(spec, 'max_roast'), 'pro_heat_pan');
});

test('warnings use recommended band, never hard limits', () => {
  assert.equal(O.warningFor(spec, 'max_roast', 'temperature_c', 250), null);
  assert.deepEqual(O.warningFor(spec, 'max_roast', 'temperature_c', 200), { min: 230, max: 370, unit: 'c' });
  assert.equal(O.warningFor(spec, 'max_roast', 'temperature_c', ''), null);
  assert.equal(O.warningFor(spec, 'pizza', 'temperature_c', 100, 'artisan'), null);
  assert.ok(O.warningFor(spec, 'pizza', 'temperature_c', 100, 'custom'));
  assert.deepEqual(O.warningFor(spec, 'gourmet_roast', 'temperature2_c', 300), { min: 95, max: 250, unit: 'c' });
  assert.ok(O.warningFor(spec, 'max_roast', 'duration_seconds', 60));
  assert.equal(O.warningFor(spec, 'keep_warm', 'temperature_c', 80), null);
});

test('hms conversion', () => {
  assert.equal(O.toSeconds(1, 30, 15), 5415);
  assert.deepEqual(O.fromSeconds(5415), { h: 1, m: 30, s: 15 });
  assert.equal(O.toSeconds('', '', ''), 0);
  assert.deepEqual(O.fromSeconds(-5), { h: 0, m: 0, s: 0 });
});

test('hebrew detection and renumbering', () => {
  assert.ok(O.hasHebrew('פיצה pizza')); assert.ok(!O.hasHebrew('pizza'));
  assert.equal(O.renumberName('stage[3].temperature_c', 1), 'stage[1].temperature_c');
  assert.equal(O.renumberName('title', 1), 'title');
});

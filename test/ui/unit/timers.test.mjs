import test from 'node:test';
import assert from 'node:assert/strict';
import * as T from '../../../web/static/js/lib/timers.js';

const T0 = 1_700_000_000_000;
const mk = (o = {}) => T.makeTimer({ id: 'a', recipeId: 'r', label: 'L', seconds: 3600, now: T0, preAlert: true, ...o });

test('pre-alert only for stage timers >= 10 min', () => {
  assert.equal(mk({ seconds: 3600 }).preAlertAt, T0 + 3600e3 - 300e3);
  assert.equal(mk({ seconds: 600 }).preAlertAt, T0 + 600e3 - 300e3);
  assert.equal(mk({ seconds: 599 }).preAlertAt, null);
  assert.equal(mk({ seconds: 3600, preAlert: false }).preAlertAt, null);
});

test('tick fires pre then end exactly once', () => {
  let list = [mk()];
  let r = T.tick(list, T0 + 10e3);
  assert.deepEqual(r.events, []);
  r = T.tick(r.timers, T0 + 3300e3);
  assert.deepEqual(r.events.map((e) => e.type), ['pre']);
  r = T.tick(r.timers, T0 + 3301e3);
  assert.deepEqual(r.events, []);
  r = T.tick(r.timers, T0 + 3600e3);
  assert.deepEqual(r.events.map((e) => e.type), ['end']);
  r = T.tick(r.timers, T0 + 3700e3);
  assert.deepEqual(r.events, []);
});

test('overdue timer on load fires only the final alert', () => {
  const r = T.tick([mk()], T0 + 4000e3);
  assert.deepEqual(r.events.map((e) => e.type), ['end']);
});

test('gourmet roast notice at end of stage 1', () => {
  const t = mk({ seconds: 4800, stage1Seconds: 1200, noticeText: 'Stage 2 now: 140' });
  assert.equal(t.noticeAt, T0 + 1200e3);
  const r = T.tick([t], T0 + 1200e3);
  assert.deepEqual(r.events.map((e) => e.type), ['notice']);
  assert.equal(r.events[0].timer.noticeText, 'Stage 2 now: 140');
});

test('format remaining', () => {
  assert.equal(T.formatRemaining(0), '0:00');
  assert.equal(T.formatRemaining(61_000), '1:01');
  assert.equal(T.formatRemaining(3_661_000), '1:01:01');
  assert.equal(T.formatRemaining(500), '0:01');
});

test('load/save round trip and pruning of old timers', () => {
  const store = new Map();
  const storage = { getItem: (k) => store.get(k) ?? null, setItem: (k, v) => store.set(k, v) };
  const fresh = mk({ now: T0 });
  const old = { ...mk({ id: 'b' }), endsAt: T0 - 13 * 3600e3, startedAt: T0 - 14 * 3600e3 };
  T.save(storage, [fresh, old]);
  const loaded = T.load(storage, T0 + 1000);
  assert.deepEqual(loaded.map((t) => t.id), ['a']);
  storage.setItem(T.STORAGE_KEY, '{not json');
  assert.deepEqual(T.load(storage, T0), []);
});

test('repeat chime every 10s for 2 minutes', () => {
  assert.equal(T.shouldRepeatChime(T0, T0, T0 + 5e3), false);
  assert.equal(T.shouldRepeatChime(T0, T0, T0 + 10e3), true);
  assert.equal(T.shouldRepeatChime(T0, T0 + 110e3, T0 + 121e3), false);
  assert.equal(T.shouldRepeatChime(T0, T0 + 100e3, T0 + 125e3), false);
});

test('removeTimer', () => {
  assert.deepEqual(T.removeTimer([mk(), mk({ id: 'z' })], 'a').map((t) => t.id), ['z']);
});

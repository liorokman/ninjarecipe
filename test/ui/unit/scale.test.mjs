import test from 'node:test';
import assert from 'node:assert/strict';
import * as S from '../../../web/static/js/lib/scale.js';
import * as K from '../../../web/static/js/lib/keys.js';

test('formatQty fractions for spoon and piece units', () => {
  assert.deepEqual(S.formatQty(0.5, 'tsp'), { text: '½', unit: 'tsp' });
  assert.deepEqual(S.formatQty(1.5, 'tbsp'), { text: '1 ½', unit: 'tbsp' });
  assert.deepEqual(S.formatQty(2.25, 'pcs'), { text: '2 ¼', unit: 'pcs' });
  assert.deepEqual(S.formatQty(1 / 3, 'tsp'), { text: '⅓', unit: 'tsp' });
  assert.deepEqual(S.formatQty(3, 'pcs'), { text: '3', unit: 'pcs' });
});
test('formatQty decimals and unit conversion', () => {
  assert.deepEqual(S.formatQty(250, 'g'), { text: '250', unit: 'g' });
  assert.deepEqual(S.formatQty(1500, 'g'), { text: '1.5', unit: 'kg' });
  assert.deepEqual(S.formatQty(1000, 'ml'), { text: '1', unit: 'l' });
  assert.deepEqual(S.formatQty(0.333333, 'g'), { text: '0.33', unit: 'g' });
  assert.deepEqual(S.formatQty(2.5, 'g'), { text: '2.5', unit: 'g' });
});
test('scaleAmount', () => {
  assert.equal(S.scaleAmount(200, 4, 6), 300);
  assert.equal(S.scaleAmount(200, 0, 6), 200);
});
test('normalizeKeys', () => {
  assert.deepEqual(K.normalizeKeys({ RecipeID: 'a', stage_1_seconds: 3, Stages: [{ TotalSeconds: 1 }] }),
    { recipeid: 'a', stage1seconds: 3, stages: [{ totalseconds: 1 }] });
});
test('golden cases shared with Go', () => {
  assert.deepEqual(S.formatQty(250, 'g'), { text: '250', unit: 'g' });
  assert.deepEqual(S.formatQty(1000, 'g'), { text: '1', unit: 'kg' });
  assert.deepEqual(S.formatQty(1500, 'ml'), { text: '1.5', unit: 'l' });
  assert.deepEqual(S.formatQty(0.5, 'tsp'), { text: '½', unit: 'tsp' });
  assert.deepEqual(S.formatQty(1.25, 'tbsp'), { text: '1 ¼', unit: 'tbsp' });
  assert.deepEqual(S.formatQty(2.333, 'pcs'), { text: '2 ⅓', unit: 'pcs' });
  assert.deepEqual(S.formatQty(0.5, 'pinch'), { text: '½', unit: 'pinch' });
  assert.deepEqual(S.formatQty(125, 'g'), { text: '125', unit: 'g' });
  assert.deepEqual(S.formatQty(0.125, 'kg'), { text: '0.13', unit: 'kg' });
});

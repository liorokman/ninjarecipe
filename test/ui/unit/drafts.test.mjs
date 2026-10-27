import test from 'node:test';
import assert from 'node:assert/strict';
import * as D from '../../../web/static/js/lib/drafts.js';

function mem() {
  const m = new Map();
  return { getItem: (k) => m.get(k) ?? null, setItem: (k, v) => m.set(k, String(v)), removeItem: (k) => m.delete(k),
    key: (i) => [...m.keys()][i] ?? null, get length() { return m.size; } };
}

test('keys', () => {
  assert.equal(D.draftKey('u1', D.targetFor('new')), 'nwr_draft:u1:new');
  assert.equal(D.draftKey('u1', D.targetFor('inbox', 'x9')), 'nwr_draft:u1:inbox:x9');
  assert.equal(D.draftKey('u1', D.targetFor('edit', 'r5')), 'nwr_draft:u1:r5');
});

test('save, load, clear', () => {
  const s = mem(); const k = D.draftKey('u', 'new');
  assert.equal(D.loadDraft(s, k), null);
  D.saveDraft(s, k, { baseVersion: 3, fields: [['title', 'Pizza']] }, 1000);
  const d = D.loadDraft(s, k);
  assert.equal(d.baseVersion, 3); assert.equal(d.savedAt, 1000); assert.deepEqual(d.fields, [['title', 'Pizza']]);
  D.clearDraft(s, k);
  assert.equal(D.loadDraft(s, k), null);
});

test('corrupt draft is ignored', () => {
  const s = mem(); s.setItem('nwr_draft:u:new', '{{{');
  assert.equal(D.loadDraft(s, 'nwr_draft:u:new'), null);
});

test('prune drops drafts older than 30 days only', () => {
  const s = mem(); const now = 100 * 86400e3;
  D.saveDraft(s, 'nwr_draft:u:a', { fields: [] }, now - 31 * 86400e3);
  D.saveDraft(s, 'nwr_draft:u:b', { fields: [] }, now - 29 * 86400e3);
  s.setItem('other', 'x');
  assert.equal(D.pruneDrafts(s, now), 1);
  assert.equal(D.loadDraft(s, 'nwr_draft:u:a'), null);
  assert.ok(D.loadDraft(s, 'nwr_draft:u:b'));
  assert.equal(s.getItem('other'), 'x');
});

test('sign-out removes only that user drafts', () => {
  const s = mem();
  D.saveDraft(s, 'nwr_draft:u1:new', { fields: [] });
  D.saveDraft(s, 'nwr_draft:u1:r1', { fields: [] });
  D.saveDraft(s, 'nwr_draft:u2:new', { fields: [] });
  s.setItem('nwr_theme', 'dark');
  assert.equal(D.clearUserDrafts(s, 'u1'), 2);
  assert.ok(D.loadDraft(s, 'nwr_draft:u2:new'));
  assert.equal(s.getItem('nwr_theme'), 'dark');
});

test('staleness and meaningful content', () => {
  assert.equal(D.isStale({ baseVersion: 2 }, 3), true);
  assert.equal(D.isStale({ baseVersion: 3 }, 3), false);
  assert.equal(D.isStale({ baseVersion: 0 }, 0), false);
  assert.equal(D.hasMeaningfulContent([['title', '  ']]), false);
  assert.equal(D.hasMeaningfulContent([['title', 'x']]), true);
});

test('collectFields skips files, disabled, unchecked', () => {
  const els = [
    { name: 'title', value: 'A', type: 'text' },
    { name: 'f', value: '', type: 'file' },
    { name: 'x', value: '1', type: 'checkbox', checked: false },
    { name: 'y', value: '1', type: 'checkbox', checked: true },
    { name: 'd', value: '1', type: 'text', disabled: true },
    { name: '', value: '1', type: 'text' },
  ];
  assert.deepEqual(D.collectFields({ elements: els }), [['title', 'A'], ['y', '1']]);
});

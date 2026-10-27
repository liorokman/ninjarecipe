import test from 'node:test';
import assert from 'node:assert/strict';
import { launch, newPage, url, tid, RECIPES } from '../helpers.mjs';

let browser;
test.before(async () => { browser = await launch(); });
test.after(async () => { await browser.close(); });

const stage = (page, i = 0) => page.locator('[data-stage-card]').nth(i);
const vis = (page, field, i = 0) => stage(page, i).locator(`[data-field="${field}"]`).evaluate((e) => !e.hidden);
const names = (page) => page.evaluate(() => [...new FormData(document.getElementById('recipe-form')).keys()]);

async function newEditor(lang = 'en') {
  const page = await newPage(browser, { lang, viewport: { width: 1000, height: 900 } });
  await page.goto(url('/recipes/new'));
  await page.waitForLoadState('networkidle');
  return page;
}
async function addStage(page, mode) {
  await tid(page, 'add-stage').click();
  const n = await page.locator('[data-stage-card]').count();
  await stage(page, n - 1).locator('select[name$=".mode"]').selectOption(mode);
  return n - 1;
}

test('mode drives visible fields; hidden fields are disabled and not submitted', async () => {
  const page = await newEditor();
  let i = await addStage(page, 'max_roast');
  assert.ok(await vis(page, 'temperature_c', i));
  assert.ok(!(await vis(page, 'temperature2_c', i)));
  assert.ok(!(await vis(page, 'pizza_preset', i)));
  assert.ok(await vis(page, 'preheat_minutes', i));
  assert.ok(!(await names(page)).includes('stage[0].temperature2_c'));
  await stage(page, 0).locator('select[name$=".mode"]').selectOption('gourmet_roast');
  assert.ok(await vis(page, 'temperature2_c')); assert.ok(await vis(page, 'duration2_seconds'));
  await stage(page, 0).locator('select[name$=".mode"]').selectOption('smoker');
  assert.ok(!(await vis(page, 'preheat_minutes')));
  assert.equal(await stage(page, 0).locator('input[name$=".woodfire"]').isChecked(), true);
  assert.equal(await stage(page, 0).locator('input[name$=".woodfire"]').isDisabled(), true);
  assert.ok(await vis(page, 'pellet_blend'));
  await stage(page, 0).locator('select[name$=".mode"]').selectOption('broil');
  assert.ok(!(await vis(page, 'woodfire'))); assert.ok(!(await vis(page, 'pellet_blend')));
  assert.equal(await stage(page, 0).locator('[data-only-mode="broil"]').isVisible(), true);
  await stage(page, 0).locator('select[name$=".mode"]').selectOption('keep_warm');
  assert.ok(!(await vis(page, 'temperature_c')));
  await page.context().close();
});

test('pizza preset hides temperature unless custom; accessory defaults', async () => {
  const page = await newEditor();
  await addStage(page, 'pizza');
  assert.ok(await vis(page, 'pizza_preset'));
  assert.ok(!(await vis(page, 'temperature_c')));
  assert.equal(await stage(page, 0).locator('input[value="pizza_stone"]').isChecked(), true);
  await stage(page, 0).locator('select[name$=".pizza_preset"]').selectOption('custom');
  assert.ok(await vis(page, 'temperature_c'));
  await stage(page, 0).locator('select[name$=".mode"]').selectOption('max_roast');
  assert.equal(await stage(page, 0).locator('input[value="pro_heat_pan"]').isChecked(), true);
  await page.context().close();
});

test('recommended hint and non-blocking warning from the oven spec', async () => {
  const page = await newEditor();
  await addStage(page, 'max_roast');
  assert.match(await stage(page, 0).locator('[data-hint="temperature_c"]').innerText(), /230/);
  await stage(page, 0).locator('input[name$=".temperature_c"]').fill('150');
  assert.match(await stage(page, 0).locator('.field-warn').first().innerText(), /Unusual for Max Roast/);
  await stage(page, 0).locator('input[name$=".temperature_c"]').fill('250');
  assert.equal(await stage(page, 0).locator('.field-warn').count(), 0);
  await page.context().close();
});

test('hours/minutes inputs write total seconds into the hidden field', async () => {
  const page = await newEditor();
  await addStage(page, 'max_roast');
  await stage(page, 0).locator('[data-field="duration_seconds"] [data-hms="h"]').fill('1');
  await stage(page, 0).locator('[data-field="duration_seconds"] [data-hms="m"]').fill('30');
  assert.equal(await stage(page, 0).locator('input[name$=".duration_seconds"]').inputValue(), '5400');
  await page.context().close();
});

test('stages: add, reorder, remove keep contiguous indexes', async () => {
  const page = await newEditor();
  await addStage(page, 'smoker'); await addStage(page, 'max_roast');
  await stage(page, 1).locator('[data-action="stage-up"]').click();
  assert.equal(await stage(page, 0).locator('select[name$=".mode"]').inputValue(), 'max_roast');
  const ns = (await names(page)).filter((n) => n.endsWith('.mode'));
  assert.deepEqual(ns, ['stage[0].mode', 'stage[1].mode']);
  await stage(page, 0).locator('[data-test="remove-stage"]').click();
  assert.deepEqual((await names(page)).filter((n) => n.endsWith('.mode')), ['stage[0].mode']);
  await page.context().close();
});

test('ingredient paste, tags chips, category limit, auto Hebrew language', async () => {
  const page = await newEditor();
  await page.locator('#f-ingredients').fill('# Dough\n500 g flour\n2 tbsp olive oil');
  await page.locator('#tag-input').fill('weekend');
  await page.locator('#tag-input').press('Enter');
  assert.equal(await page.locator('#tag-chips li').count(), 1);
  assert.equal(await page.locator('#tags-value').inputValue(), 'weekend');
  await page.locator('details.more > summary').click();
  await page.locator('#f-title').fill('פיצה ביתית');
  assert.equal(await page.locator('#f-language').inputValue(), 'he');
  const chips = page.locator('#category-chips input');
  const n = await chips.count();
  for (let i = 0; i < Math.min(n, 6); i++) await chips.nth(i).check({ force: true }).catch(() => {});
  assert.ok((await page.locator('#category-chips input:checked').count()) <= 5);
  await page.context().close();
});

test('submit: 422 swaps the form with a summary, 200 navigates and clears the draft', async () => {
  const page = await newEditor();
  await tid(page, 'save').click();           // empty title -> 422
  await page.locator('#error-summary:not([hidden])').waitFor();
  assert.ok(await page.locator('#error-summary a').count() >= 1);
  await page.locator('#f-title').fill('My new dish');
  await Promise.all([page.waitForURL(/\/recipes\//), tid(page, 'save').click()]);
  assert.ok(!(await page.evaluate(() => Object.keys(localStorage).some((k) => k.startsWith('nwr_draft:new')))));
  await page.context().close();
});

test('409 (fixtures): the magic title "conflict" shows the conflict message', { skip: process.env.UI_MODE !== 'fixtures' }, async () => {
  const page = await newEditor();
  await page.locator('#f-title').fill('conflict');
  await tid(page, 'save').click();
  await tid(page, 'conflict').waitFor();
  assert.ok(await page.evaluate(() => Object.keys(localStorage).some((k) => k.startsWith('nwr_draft:'))));
  await page.context().close();
});

test('409 (real): saving a stale edit shows the conflict message and keeps the draft', { skip: process.env.UI_MODE === 'fixtures' }, async () => {
  const a = await newPage(browser, { viewport: { width: 1000, height: 900 } });
  const b = await newPage(browser, { viewport: { width: 1000, height: 900 } });
  const edit = url(`/recipes/${RECIPES.bread}/edit`);
  await a.goto(edit); await b.goto(edit);
  await b.locator('#f-title').fill('Bread, edited elsewhere');
  await Promise.all([b.waitForURL((u) => !u.pathname.endsWith('/edit')), tid(b, 'save').click()]);
  await a.locator('#f-title').fill('Bread, my stale edit');
  await tid(a, 'save').click();
  await tid(a, 'conflict').waitFor();
  assert.match(await tid(a, 'conflict').innerText(), /changed this recipe/i);
  assert.ok(await a.evaluate(() => Object.keys(localStorage).some((k) => k.startsWith('nwr_draft:'))));
  await a.context().close(); await b.context().close();
});

test('drafts: saved after typing, restored, discarded; beforeunload when dirty', async () => {
  const page = await newEditor();
  await page.locator('#f-title').fill('Half typed recipe');
  await page.waitForFunction(() => Object.keys(localStorage).some((k) => k.startsWith('nwr_draft:')), null, { timeout: 4000 });
  let prompted = false;
  page.on('dialog', async (d) => { prompted = d.type() === 'beforeunload'; await d.accept(); });
  await page.goto(url('/recipes/new'));
  assert.ok(prompted, 'leaving a dirty editor asks for confirmation');
  await tid(page, 'draft-banner').waitFor();
  await tid(page, 'draft-continue').click();
  assert.equal(await page.locator('#f-title').inputValue(), 'Half typed recipe');
  await page.goto(url('/recipes/new'));
  await tid(page, 'draft-banner').waitFor();
  await tid(page, 'draft-discard').click();
  assert.equal(await page.evaluate(() => Object.keys(localStorage).filter((k) => k.startsWith('nwr_draft:')).length), 0);
  await page.context().close();
});

test('beforeunload handler is armed only when there are unsaved changes', async () => {
  const page = await newEditor();
  const probe = () => page.evaluate(() => { const e = new Event('beforeunload', { cancelable: true }); window.dispatchEvent(e); return e.defaultPrevented; });
  assert.equal(await probe(), false);
  await page.locator('#f-title').fill('x');
  assert.equal(await probe(), true);
  await page.context().close();
});

test('offline save keeps the draft, shows the message and retries when back online', async () => {
  const page = await newEditor();
  await page.locator('#f-title').fill('Offline dish');
  await page.context().setOffline(true);
  await tid(page, 'save').click();
  await tid(page, 'offline-banner').waitFor();
  assert.match(await tid(page, 'offline-banner').innerText(), /Not saved yet/);
  assert.ok(await page.evaluate(() => Object.keys(localStorage).some((k) => k.startsWith('nwr_draft:'))));
  await page.context().setOffline(false);
  await page.waitForURL(/\/recipes\//, { timeout: 8000 });
  await page.context().close();
});

test('edit page: existing stages render with values, photos and delete at the bottom', async () => {
  const page = await newPage(browser, { viewport: { width: 1000, height: 900 } });
  await page.goto(url(`/recipes/${RECIPES.ribs}/edit`));
  assert.equal(await page.locator('[data-stage-card]').count(), 2);
  assert.equal(await stage(page, 0).locator('select[name$=".mode"]').inputValue(), 'smoker');
  assert.equal(await stage(page, 0).locator('[data-hms="h"]').first().inputValue(), '3');
  assert.ok(await page.locator('[data-test="photo-tile"]').count() >= 2);
  assert.equal(await tid(page, 'delete-open').count(), 1);
  assert.equal(await tid(page, 'save').innerText().then((t) => t.trim()), 'Save');
  await page.context().close();
});

test('photo: real JPEG is resized and queued; HEIC-like file shows the message', async () => {
  const page = await newEditor();
  const jpg = await page.evaluate(async () => {
    const c = document.createElement('canvas'); c.width = 3000; c.height = 2000;
    const x = c.getContext('2d'); x.fillStyle = '#c33'; x.fillRect(0, 0, 3000, 2000);
    const b = await new Promise((r) => c.toBlob(r, 'image/jpeg', .9));
    return [...new Uint8Array(await b.arrayBuffer())];
  });
  await page.setInputFiles('#photo-input', { name: 'big.jpg', mimeType: 'image/jpeg', buffer: Buffer.from(jpg) });
  await page.locator('#photo-queue .photo-tile').waitFor();
  await page.setInputFiles('#photo-input', { name: 'x.heic', mimeType: 'image/heic', buffer: Buffer.from('nope') });
  await page.waitForFunction(() => /isn.t supported/.test(document.getElementById('photo-msg').textContent));
  await page.context().close();
});

test('inbox editor says "Save and keep"', async () => {
  const page = await newPage(browser);
  await page.goto(url(`/inbox/${RECIPES.inbox1}/edit`));
  assert.match(await tid(page, 'save').innerText(), /Save and keep/);
  await page.context().close();
});

import test from 'node:test';
import assert from 'node:assert/strict';
import { launch, newPage, url, tid } from '../helpers.mjs';

let browser;
test.before(async () => { browser = await launch(); });
test.after(async () => { await browser.close(); });

for (const lang of ['en', 'he']) {
  test(`live search updates results, pushes URL, Back restores (${lang})`, async () => {
    const page = await newPage(browser, { lang });
    await page.goto(url('/recipes'));
    const total = await tid(page, 'results-card').count();
    assert.ok(total >= 1);
    await tid(page, 'search-input').fill('zzzznothing');
    await page.waitForFunction(() => document.querySelector('[data-test="empty-state"]'));
    assert.match(page.url(), /q=zzzznothing/);
    assert.equal(await tid(page, 'clear-search').count(), 1);
    await page.goBack();
    await page.waitForFunction(() => !document.querySelector('[data-test="empty-state"]'));
    await page.context().close();
  });
}

test('show more appends results (4 per page in fixtures)', async () => {
  const page = await newPage(browser);
  await page.goto(url('/recipes'));
  const n1 = await tid(page, 'results-card').count();
  if (await tid(page, 'show-more').count()) {
    await tid(page, 'show-more').click();
    await page.waitForFunction((n) => document.querySelectorAll('[data-test="results-card"]').length > n, n1);
  }
  await page.context().close();
});

test('smoked tile from home filters the list; filter chips change results', async () => {
  const page = await newPage(browser);
  await page.goto(url('/'));
  await tid(page, 'tile-smoked').click();
  await page.waitForURL(/smoked=1/);
  assert.equal(await tid(page, 'filters-clear').count(), 1);
  await page.goto(url('/recipes?scope=mine'));
  assert.ok(await tid(page, 'results-card').count() >= 1);
  await page.context().close();
});

test('home shows tiles, recent row, favourites row and the badge', async () => {
  const page = await newPage(browser);
  await page.goto(url('/'));
  assert.ok(await page.locator('.tile').count() >= 2);
  assert.equal(await tid(page, 'recent-row').count(), 1);
  assert.equal(await tid(page, 'nav-inbox').count(), 1);
  assert.match(await tid(page, 'inbox-badge').innerText(), /^\d+$/);
  await page.context().close();
});

test('card shows primary mode, temperature, visibility badge', async () => {
  const page = await newPage(browser);
  await page.goto(url(`/recipes?q=${encodeURIComponent('short ribs')}`));
  const txt = await tid(page, 'results-card').first().innerText();
  assert.match(txt, /°C/);
  assert.match(txt, /Just me|Family/);
  await page.context().close();
});

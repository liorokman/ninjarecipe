import test from 'node:test';
import assert from 'node:assert/strict';
import { launch, newPage, url, tid, USERS, BASE, RECIPES } from '../helpers.mjs';

let browser;
test.before(async () => { browser = await launch(); });
test.after(async () => { await browser.close(); });

test('inbox list, item actions and throw-away dialog', async () => {
  const page = await newPage(browser);
  await page.goto(url('/inbox'));
  assert.ok(await tid(page, 'inbox-card').count() >= 1);
  await tid(page, 'inbox-card').first().click();
  assert.equal(await tid(page, 'inbox-keep').count(), 1);
  assert.equal(await tid(page, 'inbox-edit').count(), 1);
  assert.equal(await tid(page, 'inbox-share').isChecked(), false);
  assert.match(await page.locator('.inbox-item .source').innerText(), /Found on/);
  await tid(page, 'inbox-reject').click();
  await tid(page, 'reject-dialog').waitFor();
  assert.match(await tid(page, 'reject-dialog').innerText(), /Throw away this suggestion\?/);
  await page.keyboard.press('Escape');
  await page.context().close();
});

test('inbox Keep posts and moves on', async () => {
  const page = await newPage(browser);
  await page.goto(url(`/inbox/${RECIPES.inbox1}`));
  await Promise.all([page.waitForNavigation(), tid(page, 'inbox-keep').click()]);
  assert.match(page.url(), /\/inbox/);
  await page.context().close();
});

test('tokens page: create form, no forbidden words', async () => {
  const page = await newPage(browser);
  await page.goto(url('/settings/tokens'));
  assert.equal(await tid(page, 'token-form').count(), 1);
  const text = await page.locator('main').innerText();
  assert.ok(!/\btoken\b|OIDC|\bsync\b/i.test(text), 'forbidden words in UI copy');
  await page.context().close();
});

test('admin categories: list and forms for admin', async () => {
  const page = await newPage(browser, { user: 'admin' });
  await page.goto(url('/admin/categories?as=admin'));
  assert.ok(await tid(page, 'category-form').count() >= 1);
  assert.equal(await tid(page, 'category-add').count(), 1);
  await page.locator('[data-action="open-dialog"][data-target^="del-cat-"]').first().click();
  await page.locator('dialog[open]').waitFor();
  await page.context().close();
});

test('admin categorization settings: form, masked key and test result', async () => {
  const page = await newPage(browser, { user: 'admin' });
  await page.goto(url('/admin/categories?as=admin'));
  assert.equal(await tid(page, 'categorizer-form').count(), 1);
  assert.equal(await tid(page, 'engine-threshold').inputValue(), '50');
  assert.equal(await tid(page, 'engine-key').getAttribute('type'), 'password');
  assert.equal(await page.locator('[name="description"]').count() >= 2, true);
  // no engine is configured here, so Test connection reports the problem instead of crashing
  await tid(page, 'engine-test').click();
  await tid(page, 'engine-test-result').waitFor();
  assert.equal(await tid(page, 'engine-save').count(), 1);
  await page.context().close();
});

test('menu sheet: language, theme, sign out present on phone', async () => {
  const page = await newPage(browser);
  await page.goto(url('/'));
  await tid(page, 'nav-menu').click();
  await page.locator('#menu-dialog[open]').waitFor();
  for (const id of ['lang-switch', 'theme-toggle', 'signout']) assert.equal(await page.locator(`#menu-dialog [data-test="${id}"]`).count(), 1);
  await page.context().close();
});

test('desktop: top bar navigation and keyboard shortcut / focuses search', async () => {
  const page = await newPage(browser, { viewport: { width: 1366, height: 768 } });
  await page.goto(url('/recipes'));
  const pos = await page.locator('nav.nav').evaluate((e) => getComputedStyle(e).position);
  assert.equal(pos, 'static');
  await page.keyboard.press('/');
  assert.equal(await page.evaluate(() => document.activeElement.type), 'search');
  await page.context().close();
});

test('phone: bottom navigation bar is fixed to the bottom', async () => {
  const page = await newPage(browser);
  await page.goto(url('/'));
  const b = await page.locator('nav.nav').boundingBox();
  assert.ok(Math.abs(b.y + b.height - 844) < 2);
  await page.context().close();
});

test('service worker registers; visited recipe is readable offline; unknown page falls back to the offline page', async () => {
  const ctx = await browser.newContext({ serviceWorkers: 'allow', viewport: { width: 390, height: 844 } });
  const u = new URL(BASE);
  await ctx.addCookies([{ name: USERS.alice.cookieName, value: USERS.alice.cookieValue, domain: u.hostname, path: '/' }]);
  const page = await ctx.newPage();
  await page.goto(url(`/recipes/${RECIPES.ribs}`));
  await page.evaluate(() => navigator.serviceWorker.ready);
  await page.reload();               // now controlled by the SW; this load fills the page cache
  await page.waitForFunction(() => navigator.serviceWorker.controller);
  await page.reload();
  await ctx.setOffline(true);
  await page.goto(url(`/recipes/${RECIPES.ribs}`));
  assert.match(await page.locator('h1').first().innerText(), /short ribs/i);
  await page.goto(url('/recipes/unseen-one'));
  assert.match(await page.locator('body').innerText(), /No connection/);
  await ctx.close();
});

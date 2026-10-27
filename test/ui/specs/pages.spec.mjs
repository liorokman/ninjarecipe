import test from 'node:test';
import assert from 'node:assert/strict';
import { launch, newPage, url, noHScroll, RECIPES } from '../helpers.mjs';

let browser;
test.before(async () => { browser = await launch(); });
test.after(async () => { await browser.close(); });

const pages = [
  ['home', '/'], ['list', '/recipes'], ['recipe', `/recipes/${RECIPES.ribs}`], ['gourmet', `/recipes/${RECIPES.chicken}`],
  ['cook', `/recipes/${RECIPES.ribs}/cook`], ['new', '/recipes/new'], ['edit', `/recipes/${RECIPES.ribs}/edit`],
  ['inbox', '/inbox'], ['inbox-item', `/inbox/${RECIPES.inbox1}`], ['inbox-edit', `/inbox/${RECIPES.inbox1}/edit`],
  ['settings', '/settings'], ['tokens', '/settings/tokens'], ['signin', '/signin'], ['loggedout', '/auth/logged-out'], ['404', '/recipes/nope'],
];

for (const lang of ['en', 'he']) {
  for (const [name, p] of pages) {
    test(`${name} (${lang}): no CSP violation, no console error, no inline style/handlers, labelled inputs`, async () => {
      const page = await newPage(browser, { lang });
      await page.goto(url(p));
      await page.waitForLoadState('networkidle');
      assert.deepEqual(await page.csp(), []);
      assert.deepEqual(page.problems.filter((x) => !/404|Failed to load resource/.test(x)), []);
      const bad = await page.evaluate(() => ({
        style: document.querySelectorAll('[style]').length,
        handlers: [...document.querySelectorAll('*')].filter((e) => [...e.attributes].some((a) => /^on/i.test(a.name))).length,
        inlineScripts: [...document.querySelectorAll('script:not([src])')].filter((s) => s.type !== 'application/json').length,
        unlabelled: [...document.querySelectorAll('input:not([type=hidden]):not([type=submit]):not([type=file]), select, textarea')].filter((i) => {
          if (i.disabled && i.closest('[hidden]')) return false;
          return !(i.labels && i.labels.length) && !i.getAttribute('aria-label') && !i.getAttribute('aria-labelledby');
        }).map((i) => i.name || i.id || i.type),
      }));
      assert.equal(bad.style, 0); assert.equal(bad.handlers, 0); assert.equal(bad.inlineScripts, 0);
      assert.deepEqual(bad.unlabelled.filter((n) => !/hms|^$/.test(n)), []);
      assert.equal(await page.evaluate(() => document.documentElement.dir), lang === 'he' ? 'rtl' : 'ltr');
      await page.context().close();
    });
  }
}

for (const [w, h] of [[360, 740], [820, 1180], [1366, 768]]) {
  test(`no horizontal scroll at ${w}x${h}`, async () => {
    for (const lang of ['en', 'he']) {
      for (const [name, p] of pages.filter(([n]) => !['404'].includes(n))) {
        const page = await newPage(browser, { lang, viewport: { width: w, height: h } });
        await page.goto(url(p)); await page.waitForLoadState('networkidle');
        assert.ok(await noHScroll(page), `${name} ${lang} scrolls horizontally at ${w}`);
        await page.context().close();
      }
    }
  });
}

test('theme toggle persists and sets data-theme', async () => {
  const page = await newPage(browser);
  await page.goto(url('/settings'));
  await page.locator('[data-test="theme-toggle"]').first().click();
  assert.equal(await page.evaluate(() => document.documentElement.dataset.theme), 'light');
  await page.locator('[data-test="theme-toggle"]').first().click();
  assert.equal(await page.evaluate(() => document.documentElement.dataset.theme), 'dark');
  await page.reload();
  assert.equal(await page.evaluate(() => document.documentElement.dataset.theme), 'dark');
  assert.equal(await page.evaluate(() => localStorage.getItem('nwr_theme')), 'dark');
  await page.context().close();
});

test('language switch to Hebrew mirrors the layout', async () => {
  const page = await newPage(browser);
  await page.goto(url('/settings'));
  await page.locator('[data-test="lang-switch"]').first().click();
  await page.waitForLoadState('networkidle');
  assert.equal(await page.evaluate(() => document.documentElement.lang), 'he');
  assert.equal(await page.evaluate(() => document.documentElement.dir), 'rtl');
  // the language is stored on the user: restore English so later tests see the default
  await page.request.post(url('/settings/language'), { form: { lang: 'en', return_to: '/' } });
  await page.context().close();
});

test('signed-out: sign-in button and no nav', async () => {
  const page = await newPage(browser, { user: 'none' });
  await page.goto(url('/signin?as=none')); // ?as=none is the fixtures harness switch; real mode is signed out by default
  assert.equal(await page.locator('[data-test="signin-button"]').count(), 1);
  assert.equal(await page.locator('nav.nav').count(), 0);
  await page.context().close();
});

test('PWA: manifest, icons and offline page reachable', async () => {
  const page = await newPage(browser);
  const m = await (await page.request.get(url('/manifest.webmanifest'))).json();
  assert.equal(m.display, 'standalone'); assert.equal(m.start_url, '/');
  for (const i of m.icons) assert.equal((await page.request.get(url(i.src))).status(), 200);
  const sw = await page.request.get(url('/sw.js'));
  assert.equal(sw.status(), 200);
  assert.ok(!(await sw.text()).includes('__ASSET_VERSION__'));
  await page.context().close();
});

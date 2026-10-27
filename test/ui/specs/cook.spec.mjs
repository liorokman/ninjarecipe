import test from 'node:test';
import assert from 'node:assert/strict';
import { launch, newPage, url, tid, RECIPES } from '../helpers.mjs';

let browser;
test.before(async () => { browser = await launch(); });
test.after(async () => { await browser.close(); });

const cur = (page) => page.locator('.cook-step.is-current').getAttribute('id');
const cookURL = (id = RECIPES.ribs) => url(`/recipes/${id}/cook`);

async function open(opts = {}, id) {
  const page = await newPage(browser, opts);
  await page.addInitScript(() => {
    window.__wake = 0;
    Object.defineProperty(navigator, 'wakeLock', { value: { request: async () => { window.__wake++; return { release() {}, addEventListener() {} }; } }, configurable: true });
  });
  return page;
}

test('Next/Back, hash restore, last step opens the done dialog', async () => {
  const page = await open();
  await page.goto(cookURL());
  assert.equal(await cur(page), 'step-1');
  assert.ok(await tid(page, 'step-back').isDisabled());
  await tid(page, 'step-next').click();
  assert.equal(await cur(page), 'step-2');
  assert.match(page.url(), /#step-2$/);
  await page.reload();
  assert.equal(await cur(page), 'step-2');
  await tid(page, 'step-back').click();
  assert.equal(await cur(page), 'step-1');
  const total = await page.locator('.cook-step').count();
  for (let i = 1; i < total; i++) await tid(page, 'step-next').click();
  assert.equal(await tid(page, 'step-next').innerText().then((t) => t.trim()), 'Finish');
  await tid(page, 'step-next').click();
  await tid(page, 'done-dialog').waitFor();
  assert.equal(await tid(page, 'cook-yes').count(), 1);
  assert.equal(await tid(page, 'cook-not-now').count(), 1);
  await page.context().close();
});

test('buttons are at least 72px high; wake lock requested', async () => {
  const page = await open();
  await page.goto(cookURL());
  const box = await tid(page, 'step-next').boundingBox();
  assert.ok(box.height >= 72, `next is ${box.height}px`);
  assert.ok(await page.evaluate(() => window.__wake) >= 1);
  await page.context().close();
});

test('arrow keys and swipe, mirrored in Hebrew', async () => {
  for (const lang of ['en', 'he']) {
    const page = await open({ lang, touch: true });
    await page.goto(cookURL());
    const nextKey = lang === 'he' ? 'ArrowLeft' : 'ArrowRight';
    const backKey = lang === 'he' ? 'ArrowRight' : 'ArrowLeft';
    await page.keyboard.press(nextKey);
    assert.equal(await cur(page), 'step-2', `${lang} arrow next`);
    await page.keyboard.press(backKey);
    assert.equal(await cur(page), 'step-1');
    const swipe = (dx) => page.evaluate((d) => {
      const area = document.querySelector('[data-swipe-area]');
      const mk = (type, x) => { const t = new Touch({ identifier: 1, target: area, clientX: x, clientY: 300 }); return new TouchEvent(type, { touches: type === 'touchend' ? [] : [t], changedTouches: [t], bubbles: true, cancelable: true }); };
      area.dispatchEvent(mk('touchstart', 200)); area.dispatchEvent(mk('touchend', 200 + d));
    }, dx);
    await swipe(lang === 'he' ? 120 : -120);
    assert.equal(await cur(page), 'step-2', `${lang} swipe next`);
    await swipe(30);
    assert.equal(await cur(page), 'step-2', 'short swipe ignored');
    await swipe(lang === 'he' ? -120 : 120);
    assert.equal(await cur(page), 'step-1', `${lang} swipe back`);
    await page.context().close();
  }
});

test('ingredients panel, tick-off persists in session, scaling in panel', async () => {
  const page = await open();
  await page.goto(cookURL());
  await tid(page, 'ingredients-open').click();
  await tid(page, 'ingredients-panel').waitFor();
  const li = page.locator('#ingredients-panel li.ingredient').first();
  await li.click();
  assert.ok(await li.evaluate((e) => e.classList.contains('is-ticked')));
  await page.keyboard.press('Escape');
  await page.reload();
  await tid(page, 'ingredients-open').click();
  assert.ok(await page.locator('#ingredients-panel li.ingredient').first().evaluate((e) => e.classList.contains('is-ticked')));
  const q = page.locator('#ingredients-panel li.ingredient[data-amount] .ing-qty').first();
  const before = await q.innerText();
  await tid(page, 'servings-plus').click();
  assert.notEqual(await q.innerText(), before);
  await page.context().close();
});

test('step timer: runs, survives reload (nwr_timers), final alert banner', async () => {
  const page = await open();
  await page.clock.install({ time: new Date('2026-03-01T12:00:00Z') });
  await page.goto(cookURL() + '#step-2');
  const chip = page.locator('[data-test="timer-start"][data-action="step-timer-start"]').first();
  await chip.click();
  assert.equal(await tid(page, 'timer-tray').isVisible(), true);
  assert.equal(await page.locator('[data-test="timer-item"]').count(), 1);
  assert.ok(await page.evaluate(() => JSON.parse(localStorage.getItem('nwr_timers')).length) === 1);
  await page.clock.fastForward(30_000);
  await page.reload();
  assert.equal(await page.locator('[data-test="timer-item"]').count(), 1, 'timer survived reload');
  const secs = await page.evaluate(() => { const t = JSON.parse(localStorage.getItem('nwr_timers'))[0]; return Math.round((t.endsAt - t.startedAt) / 1000); });
  await page.clock.fastForward((secs + 5) * 1000);
  await tid(page, 'timer-banner').waitFor({ state: 'visible' });
  assert.match(await tid(page, 'timer-banner').innerText(), /time.s up/i);
  await tid(page, 'timer-dismiss').click();
  assert.equal(await tid(page, 'timer-banner').isVisible(), false);
  await page.context().close();
});

test('stage timer: 5-minute pre-alert for stages >= 10 min, then final alert; gourmet stage-2 notice', async () => {
  const page = await open();
  await page.clock.install({ time: new Date('2026-03-01T12:00:00Z') });
  await page.goto(cookURL());
  await page.locator('[data-action="stage-timer-start"]').first().click();   // smoker 3 h
  await page.clock.fastForward((3 * 3600 - 5 * 60 + 2) * 1000);
  await tid(page, 'timer-banner').waitFor({ state: 'visible' });
  assert.match(await tid(page, 'timer-banner').innerText(), /5 minutes left/);
  await tid(page, 'timer-dismiss').click();
  await page.clock.fastForward(5 * 60 * 1000);
  assert.match(await tid(page, 'timer-banner').innerText(), /time.s up/i);
  await page.context().close();

  const g = await open();
  await g.clock.install({ time: new Date('2026-03-01T12:00:00Z') });
  await g.goto(cookURL(RECIPES.chicken));
  await g.locator('[data-action="stage-timer-start"]').first().click();
  await g.clock.fastForward((20 * 60 + 2) * 1000);
  await tid(g, 'timer-banner').waitFor({ state: 'visible' });
  assert.match(await tid(g, 'timer-banner').innerText(), /140/);
  await g.context().close();
});

test('stage switcher and theme toggle work in cook mode; Done asks "Did you cook it?"', async () => {
  const page = await open();
  await page.goto(cookURL());
  assert.match(await page.locator('[data-stage-counter]').innerText(), /Stage 1 of 2/);
  await page.locator('[data-action="stage-next"]').click();
  assert.match(await page.locator('[data-stage-counter]').innerText(), /Stage 2 of 2/);
  await page.locator('.cook-top [data-test="theme-toggle"]').click();
  assert.ok(await page.evaluate(() => !!localStorage.getItem('nwr_theme')));
  await tid(page, 'cook-done').click();
  assert.match(await tid(page, 'done-dialog').innerText(), /Did you cook it\?/);
  await Promise.all([page.waitForURL(/\/recipes\/[^/]+(#.*)?$/), tid(page, 'cook-not-now').click()]);
  await page.context().close();
});

test('cook mode works offline after load (all data in the page)', async () => {
  const page = await open();
  await page.goto(cookURL());
  await page.context().setOffline(true);
  await tid(page, 'step-next').click();
  assert.equal(await cur(page), 'step-2');
  await page.context().close();
});

test('no CSP violations in cook mode', async () => {
  const page = await open();
  await page.goto(cookURL() + '#step-2'); await page.locator('[data-action="step-timer-start"]').first().click();
  assert.deepEqual(await page.csp(), []);
  assert.deepEqual(page.problems, []);
  await page.context().close();
});

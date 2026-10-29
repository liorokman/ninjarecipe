import test from 'node:test';
import assert from 'node:assert/strict';
import { launch, newPage, url, tid, noHScroll, USERS, BASE, RECIPES } from '../helpers.mjs';

let browser;
test.before(async () => { browser = await launch(); });
test.after(async () => { await browser.close(); });

const item = () => url(`/inbox/${RECIPES.inbox1}`);

for (const lang of ['en', 'he']) {
  test(`throw-away dialog (${lang}): optional reasons are single-choice chips and tapping the chosen one clears it`, async () => {
    const page = await newPage(browser, { lang, touch: true });
    await page.goto(item());
    await tid(page, 'inbox-reject').click();
    const dialog = tid(page, 'reject-dialog');
    assert.equal(await dialog.evaluate((d) => d.open), true);

    const radios = page.locator('[data-test="reject-reasons"] input[type=radio]');
    assert.equal(await radios.count(), 5);
    assert.equal(await page.locator('[data-test="reject-reasons"] input:checked').count(), 0, 'nothing is preselected');

    const chip = (r) => tid(page, `reject-reason-${r}`);
    await chip('too_complicated').locator('xpath=..').click();
    assert.equal(await chip('too_complicated').isChecked(), true);
    await chip('not_our_taste').locator('xpath=..').click();
    assert.equal(await chip('not_our_taste').isChecked(), true);
    assert.equal(await chip('too_complicated').isChecked(), false, 'single choice');
    await chip('not_our_taste').locator('xpath=..').click();
    assert.equal(await page.locator('[data-test="reject-reasons"] input:checked').count(), 0, 'tapping the selected chip clears it');

    // chips are fingertip-sized and the page does not scroll sideways
    const box = await chip('poor_details').locator('xpath=..').boundingBox();
    assert.ok(box.height >= 44, `chip height ${box.height}`);
    assert.ok(await noHScroll(page));
    assert.deepEqual(await page.csp(), []);
    assert.deepEqual(page.problems, []);
    await page.context().close();
  });
}

test('throw-away posts the chosen reason and note; nothing chosen posts neither', async () => {
  const page = await newPage(browser, { touch: true });
  const posts = [];
  await page.route('**/reject', async (route) => {
    posts.push(route.request().postData() || '');
    await route.fulfill({ status: 303, headers: { location: '/inbox' } });
  });
  await page.goto(item());
  await tid(page, 'inbox-reject').click();
  await tid(page, 'reject-reason-not_for_our_oven').locator('xpath=..').click();
  await tid(page, 'reject-note').fill('Needs a rotisserie');
  await tid(page, 'reject-confirm').click();
  await page.waitForURL(/\/inbox$/);
  assert.equal(posts.length, 1);
  const p = new URLSearchParams(posts[0]);
  assert.equal(p.get('reason'), 'not_for_our_oven');
  assert.equal(p.get('note'), 'Needs a rotisserie');

  await page.goto(item());
  await tid(page, 'inbox-reject').click();
  await tid(page, 'reject-confirm').click();
  await page.waitForURL(/\/inbox$/);
  const q = new URLSearchParams(posts[1]);
  assert.equal(q.get('reason'), null);
  assert.equal(q.get('note') || '', '');
  await page.context().close();
});

test('without JavaScript the throw-away form is shown inline and works', async () => {
  const ctx = await browser.newContext({ javaScriptEnabled: false, viewport: { width: 390, height: 844 } });
  const base = new URL(BASE);
  await ctx.addCookies([{ name: USERS.alice.cookieName, value: USERS.alice.cookieValue, domain: base.hostname, path: '/' }]);
  const page = await ctx.newPage();
  await page.goto(item());
  assert.equal(await tid(page, 'reject-form').isVisible(), true, 'dialog content is visible inline');
  assert.equal(await tid(page, 'inbox-reject').isVisible(), false, 'the open-dialog button is hidden');
  assert.equal(await tid(page, 'reject-confirm').isVisible(), true);
  await ctx.close();
});

test('assistant brief box on the access-keys page is labelled, keeps its text and fits a phone', async () => {
  const page = await newPage(browser, { touch: true });
  await page.goto(url('/settings/tokens'));
  const box = tid(page, 'brief').locator('textarea');
  assert.equal(await box.isVisible(), true);
  assert.ok((await box.getAttribute('maxlength')) === '2000');
  assert.equal(await page.getByLabel(/What should your assistant look for\?/).count(), 1);
  assert.ok(await noHScroll(page));
  assert.ok((await tid(page, 'brief-save').boundingBox()).height >= 44);
  await page.context().close();
});

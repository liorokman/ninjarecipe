import test from 'node:test';
import assert from 'node:assert/strict';
import { launch, newPage, url, tid, RECIPES } from '../helpers.mjs';

let browser;
test.before(async () => { browser = await launch(); });
test.after(async () => { await browser.close(); });

const count = async (page, id) => tid(page, id).count();

// 70 section 4.3 flag matrix. fx-ribs: alice private; fx-chicken: alice shared; fx-pizza-he: bob shared; fx-ownerless: shared.
const cases = [
  ['owner private', 'alice', 'ribs', { 'edit-link': 1, 'delete-open': 1, 'visibility-switch': 1, 'copy-form': 0, 'favourite-button': 1, 'cooklog-form': 1 }],
  ['owner shared', 'alice', 'chicken', { 'edit-link': 1, 'delete-open': 1, 'visibility-switch': 1, 'copy-form': 0, 'favourite-button': 1, 'cooklog-form': 1 }],
  ['other user shared', 'bob', 'chicken', { 'edit-link': 1, 'delete-open': 1, 'visibility-switch': 0, 'copy-form': 1, 'favourite-button': 1, 'cooklog-form': 1 }],
  ['admin shared', 'admin', 'chicken', { 'edit-link': 1, 'delete-open': 1, 'visibility-switch': 0, 'copy-form': 1, 'favourite-button': 1, 'cooklog-form': 1 }],
  ['ownerless shared', 'alice', 'ownerless', { 'edit-link': 1, 'delete-open': 1, 'visibility-switch': 0, 'copy-form': 1, 'favourite-button': 1, 'cooklog-form': 1 }],
];
for (const [name, user, rid, want] of cases) {
  test(`flags: ${name}`, async () => {
    const page = await newPage(browser, { user });
    await page.goto(url(`/recipes/${RECIPES[rid]}`));
    for (const [id, n] of Object.entries(want)) assert.equal(await count(page, id), n, `${id} for ${name}`);
    if (!want['visibility-switch']) assert.equal(await count(page, 'visibility-badge') >= 1, true);
    await page.context().close();
  });
}

test("other user's private recipe is a friendly 404", async () => {
  const page = await newPage(browser, { user: 'alice' });
  const r = await page.goto(url(`/recipes/${RECIPES.bobPrivate}`));
  assert.equal(r.status(), 404);
  assert.equal(await count(page, 'error-page'), 1);
  await page.context().close();
});

test('oven hero: smoker + max roast stages, woodfire, preheat, gourmet two phases', async () => {
  const page = await newPage(browser);
  await page.goto(url(`/recipes/${RECIPES.ribs}`));
  assert.equal(await tid(page, 'stage-card').count(), 2);
  const first = await tid(page, 'stage-card').first().innerText();
  assert.match(first, /SMOKER/i); assert.match(first, /120/);
  assert.equal(await tid(page, 'woodfire-badge').count() >= 1, true);
  await page.goto(url(`/recipes/${RECIPES.chicken}`));
  const g = await tid(page, 'stage-card').first().innerText();
  assert.match(g, /330/); assert.match(g, /140/);
  assert.equal(await tid(page, 'oil-warning').count(), 1);
  await page.context().close();
});

test('no-stage recipe shows the friendly empty hero', async () => {
  const page = await newPage(browser);
  await page.goto(url(`/recipes/${RECIPES.noStages}`));
  assert.equal(await tid(page, 'stage-card').count(), 0);
  assert.match(await page.locator('.oven-hero').innerText(), /No oven settings yet/);
  await page.context().close();
});

test('share switch both directions with wording (htmx)', async () => {
  const page = await newPage(browser);
  await page.goto(url(`/recipes/${RECIPES.bread}`));
  const sw = tid(page, 'visibility-switch');
  assert.equal(await sw.getAttribute('aria-checked'), 'false');
  await sw.click();
  await page.locator('[data-msg="on"]:not([hidden])').waitFor();
  assert.match(await page.locator('[data-msg="on"]').innerText(), /Everyone in the family will see this recipe/);
  assert.equal(await tid(page, 'visibility-switch').getAttribute('aria-checked'), 'true');
  await tid(page, 'visibility-switch').click();
  await page.locator('[data-msg="off"]:not([hidden])').waitFor();
  assert.match(await page.locator('[data-msg="off"]').innerText(), /won.t see this recipe any more/);
  await page.context().close();
});

test('favourite heart toggles without reload', async () => {
  const page = await newPage(browser);
  await page.goto(url(`/recipes/${RECIPES.bread}`));
  const before = await tid(page, 'favourite-button').getAttribute('aria-pressed');
  await tid(page, 'favourite-button').click();
  await page.waitForFunction((b) => document.querySelector('[data-test="favourite-button"]').getAttribute('aria-pressed') !== b, before);
  await page.context().close();
});

test('delete dialog: Keep it is focused, wording, family line for shared', async () => {
  const page = await newPage(browser);
  await page.goto(url(`/recipes/${RECIPES.chicken}`));
  await page.locator('summary[aria-label]').click();
  await tid(page, 'delete-open').click();
  const dlg = tid(page, 'delete-dialog');
  await dlg.waitFor();
  assert.match(await dlg.innerText(), /forever/i);
  assert.match(await dlg.innerText(), /whole family/i);
  assert.equal(await page.evaluate(() => document.activeElement.dataset.test), 'delete-keep');
  assert.equal(await tid(page, 'delete-form').locator('button').innerText(), 'Delete forever');
  await page.keyboard.press('Escape');
  await page.context().close();
});

test('cook log with a note and rating via htmx', async () => {
  const page = await newPage(browser);
  await page.goto(url(`/recipes/${RECIPES.bread}`));
  const form = tid(page, 'cooklog-form');
  await form.locator('textarea').fill('Less salt next time');
  await form.locator('.star-opt').nth(3).click();
  await form.locator('button[type=submit]').click();
  await page.locator('#cook-logs .log-note', { hasText: 'Less salt next time' }).waitFor();
  assert.equal(await tid(page, 'cooklog-delete-form').count() >= 1, true);
  await page.context().close();
});

test('servings stepper scales amounts', async () => {
  const page = await newPage(browser);
  await page.goto(url(`/recipes/${RECIPES.ribs}`));
  const first = page.locator('li.ingredient[data-unit="tbsp"] .ing-qty').first();
  const before = await first.innerText();
  await tid(page, 'servings-plus').click();
  assert.notEqual(await first.innerText(), before);
  await tid(page, 'servings-minus').click();
  assert.equal(await first.innerText(), before);
  await page.context().close();
});

test('Hebrew recipe renders RTL inside, English recipe keeps LTR inside Hebrew UI', async () => {
  const page = await newPage(browser, { lang: 'he' });
  await page.goto(url(`/recipes/${RECIPES.hebrew}`));
  assert.equal(await page.locator('h1.recipe-title').getAttribute('dir'), 'rtl');
  await page.goto(url(`/recipes/${RECIPES.ribs}`));
  assert.equal(await page.locator('h1.recipe-title').getAttribute('dir'), 'ltr');
  await page.context().close();
});

test('copy button and share link exist for others', async () => {
  const page = await newPage(browser, { user: 'bob' });
  await page.goto(url(`/recipes/${RECIPES.chicken}`));
  assert.equal(await count(page, 'share-link'), 1);
  assert.equal(await count(page, 'start-cooking'), 1);
  await page.context().close();
});

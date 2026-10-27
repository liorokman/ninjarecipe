import { chromium } from 'playwright-core';

export const BASE = process.env.UI_BASE;
export const USERS = JSON.parse(process.env.UI_USERS || '{}');
export const RECIPES = JSON.parse(process.env.UI_RECIPES || '{}');

export async function launch() {
  const opts = { channel: 'chrome' };
  if (process.env.CHROME_PATH) { delete opts.channel; opts.executablePath = process.env.CHROME_PATH; }
  if (process.env.HEADED) opts.headless = false;
  return chromium.launch(opts);
}

// Opens a context signed in as `user` (alice|bob|admin|none). Collects CSP violations and console errors in ctx.problems.
export async function newPage(browser, { user = 'alice', lang = 'en', viewport = { width: 390, height: 844 }, theme = 'light', touch = false } = {}) {
  const ctx = await browser.newContext({ viewport, colorScheme: theme, hasTouch: touch, locale: lang === 'he' ? 'he-IL' : 'en-US', serviceWorkers: 'block' });
  const base = new URL(BASE);
  const cookies = [{ name: 'lang', value: lang, domain: base.hostname, path: '/' }];
  if (user !== 'none' && USERS[user]) cookies.push({ name: USERS[user].cookieName, value: USERS[user].cookieValue, domain: base.hostname, path: '/' });
  await ctx.addCookies(cookies);
  await ctx.addInitScript(() => {
    window.__csp = [];
    document.addEventListener('securitypolicyviolation', (e) => window.__csp.push(`${e.violatedDirective} ${e.blockedURI}`));
  });
  const page = await ctx.newPage();
  const problems = [];
  page.on('console', (m) => { if (m.type() === 'error') problems.push(`console: ${m.text()}`); });
  page.on('pageerror', (e) => problems.push(`pageerror: ${e.message}`));
  page.problems = problems;
  page.csp = () => page.evaluate(() => window.__csp || []);
  return page;
}

export const url = (p) => `${BASE}${p}`;
export const tid = (page, id) => page.locator(`[data-test="${id}"]`);

export async function noHScroll(page) {
  return page.evaluate(() => document.documentElement.scrollWidth <= document.documentElement.clientWidth + 1);
}

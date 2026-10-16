const assert = require('node:assert/strict');
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || '/home/nick/goprojects/test-agent/node_modules/playwright');
const base = process.env.JERRY_SMOKE_URL;
if (!base || new URL(base).hostname !== '127.0.0.1') throw new Error('Requires disposable loopback fixture');
(async () => {
  const browser = await chromium.launch({headless: true});
  try {
    const context = await browser.newContext({baseURL: base, colorScheme: 'light'});
    // A fresh appliance must work with LAN access and no Internet connection.
    await context.route('**/*', route => new URL(route.request().url()).origin === base ? route.continue() : route.abort());
    const page = await context.newPage();
    const errors = [];
    page.on('pageerror', e => errors.push(e.message));
    assert.notEqual((await context.request.get('/api/library/status', {maxRedirects: 0})).status(), 200);
    await page.goto('/login');
    await page.getByTestId('login-user').fill('admin');
    await page.getByTestId('login-password').fill('admin');
    await page.getByTestId('login-submit').click();
    await page.waitForURL('**/change-password');
    await page.getByTestId('new-password').fill('import-fixture-password');
    await page.getByTestId('confirm-password').fill('import-fixture-password');
    await page.getByTestId('change-password-submit').click();
    await page.waitForURL(base + '/');
    for (const width of [1440, 390]) {
      await page.setViewportSize({width, height: 900});
      await page.goto('/settings');
      await page.getByRole('heading', {name: '1 of 180 episodes on SD'}).waitFor();
      let status = await (await context.request.get('/api/library/status')).json();
      assert.equal(status.present, 1); assert.equal(status.missing, 179);
      assert.equal(status.safe_to_remove, true);
      assert.ok(['copied', 'already on SD'].includes(status.results[0].status));
      assert.equal(await page.locator('html').evaluate(el => el.classList.contains('dark')), false, 'system light preference');
      await page.getByText('All 9 seasons · episode checklist', {exact: true}).click();
      assert.equal(await page.locator('[data-testid=library-checklist] li').count(), 180);
      assert.match(await page.locator('[data-testid=library-checklist] li').filter({hasText:'S09E12'}).innerText(), /On SD/);
      assert.match(await page.locator('[data-testid=library-checklist] li').filter({hasText:'S09E11'}).innerText(), /Missing/);
      await page.getByText('Fix a filename or copy an episode manually', {exact: true}).click();
      assert.ok((await page.getByTestId('library-status').innerText()).includes(process.env.JERRY_LIBRARY_PATH));
      await page.waitForTimeout(3300);
      assert.equal(await page.locator('details[data-section=episodes]').getAttribute('open'), '', 'polling preserves expanded checklist');
      assert.equal(await page.locator('#library-connection').innerText(), '');
      await page.getByTestId('library-rescan').click();
      await page.waitForURL('**/settings');
      assert.ok(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth+2), 'no horizontal overflow');
    }
    await page.evaluate(() => localStorage.setItem('jerry-theme', 'dark'));
    await page.reload();
    assert.equal(await page.locator('html').evaluate(el => el.classList.contains('dark')), true, 'saved dark preference');
    assert.deepEqual(errors, [], 'no JS errors with external network blocked');
    console.log('PASS: real USB-to-SD import, auth, 180-entry checklist, retry, LAN-only UI, light/dark and desktop/mobile');
  } finally {await browser.close();}
})().catch(e => {console.error(e);process.exitCode=1;});

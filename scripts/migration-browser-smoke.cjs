// Launched by TestMigrationBrowserSmoke against its disposable loopback server.
const assert = require('node:assert/strict');
const path = require('node:path');
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || path.resolve(__dirname, '../../test-agent/node_modules/playwright'));
const base = process.env.JERRY_SMOKE_URL;
if (!base || new URL(base).hostname !== '127.0.0.1') throw new Error('Requires disposable loopback fixture');

(async () => {
  const browser = await chromium.launch({ headless: true });
  try {
    const context = await browser.newContext({ baseURL: base });
    const page = await context.newPage();
    page.setDefaultTimeout(12000);
    const errors = [];
    page.on('pageerror', e => errors.push(e.message));
    const denied = await context.request.get('/api/events/export', { maxRedirects: 0 });
    assert.notEqual(denied.status(), 200, 'anonymous events must be denied');
    await page.goto('/login');
    await page.getByTestId('login-user').fill('admin');
    await page.getByTestId('login-password').fill('admin');
    await page.getByTestId('login-submit').click();
    await page.waitForURL('**/change-password');
    await page.getByTestId('new-password').fill('smoke-only-strong-password');
    await page.getByTestId('confirm-password').fill('smoke-only-strong-password');
    await page.getByTestId('change-password-submit').click();
    await page.waitForURL(base + '/');

    const exported = await context.request.get('/api/events/export?after=0&limit=100');
    assert.equal(exported.status(), 200);
    assert.equal((await exported.json()).durable, true);

    for (const [label, width, height] of [['desktop', 1440, 1000], ['mobile', 390, 844]]) {
      await page.setViewportSize({ width, height });
      await page.goto('/settings');
      await page.getByTestId('quiet-mode-enabled').check();
      await page.locator('[name=quiet_start]').fill('22:30');
      await page.locator('[name=quiet_end]').fill('06:30');
      await Promise.all([page.waitForNavigation(), page.getByTestId('save-settings').click()]);
      await page.reload();
      assert.equal(await page.getByTestId('quiet-mode-enabled').isChecked(), true);
      assert.equal(await page.locator('[name=quiet_start]').inputValue(), '22:30');
      assert.equal(await page.locator('[name=quiet_end]').inputValue(), '06:30');

      await page.goto('/');
      const volume = page.waitForResponse(r => r.url().endsWith('/api/volume') && r.request().method() === 'POST');
      await page.getByTestId('volume-7').click();
      assert.equal((await volume).status(), 204);
      await page.reload();
      assert.match(await page.locator('#volume-level').innerText(), /7\s*\/\s*10/);

      await page.goto('/commercials');
      // Tiny fake media exercises upload/catalog/UI, not decoding (real-mpv test owns that).
      await page.locator('[name=commercial_file]').setInputFiles({ name: `${label}.mp4`, mimeType: 'video/mp4', buffer: Buffer.from('fixture') });
      await page.locator('[name=campaign]').fill(`${label} campaign`);
      await Promise.all([page.waitForNavigation(), page.getByRole('button', { name: 'Upload commercial', exact: true }).click()]);
      const campaignForm = page.locator('form[action="/commercials/campaigns/save"]');
      assert.match(await campaignForm.innerText(), new RegExp(`${label} campaign`));
      await campaignForm.locator('input[type=number]').last().fill('3');
      await Promise.all([page.waitForNavigation(), campaignForm.getByRole('button', { name: 'Save campaigns' }).click()]);
      assert.equal(await page.locator('form[action="/commercials/campaigns/save"] input[type=number]').last().inputValue(), '3');

      await page.goto('/soundboard');
      await page.locator('[name=sound_file]').setInputFiles({ name: `${label}.wav`, mimeType: 'audio/wav', buffer: Buffer.from('fixture') });
      await Promise.all([page.waitForNavigation(), page.getByRole('button', { name: 'Upload sound', exact: true }).click()]);
      await page.getByTestId('soundboard-grid').waitFor();
      assert.ok((await page.getByTestId('soundboard-grid').innerText()).includes(label));
      for (const route of ['/', '/settings', '/commercials', '/soundboard']) {
        await page.goto(route);
        const overflow = await page.evaluate(() => ({
          width: document.documentElement.scrollWidth, viewport: innerWidth,
          elements: [...document.querySelectorAll('body *')].filter(el => el.getBoundingClientRect().right > innerWidth + 2).slice(0, 6).map(el => ({ tag: el.tagName, class: el.className, width: el.getBoundingClientRect().width }))
        }));
        assert.ok(overflow.width <= overflow.viewport + 2, `${label} horizontal overflow at ${route}: ${JSON.stringify(overflow)}`);
      }
      console.log(`${label}: settings, volume, uploads, campaigns, soundboard and layout passed`);
    }
    assert.deepEqual(errors, [], 'browser runtime errors');
    console.log('PASS: login, password rotation, event-export authorization; no browser runtime errors');
  } finally { await browser.close(); }
})().catch(e => { console.error(e); process.exitCode = 1; });

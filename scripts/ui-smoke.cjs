const assert = require('node:assert/strict');
const { chromium } = require('playwright');
const { authenticate, logout, owner } = require('./browser-account.cjs');

(async () => {
  const base = 'http://127.0.0.1:9080';
  const browser = await chromium.launch({ headless: true, executablePath: process.env.MCM_CHROMIUM });
  try {
    const page = await browser.newPage({ viewport: { width: 1280, height: 900 } });
    const errors = [];
    page.on('pageerror', (e) => errors.push(e.message));
    page.on('dialog', (d) => d.accept());
    await authenticate(page, base, owner, process.env.MCM_UI_KEY);
    const go = async (path) => {
      await page.goto(base + path);
      if (path.startsWith('/nodes'))
        await page.waitForFunction(() => document.querySelector('#node')?.value === 'local');
    };
    const output = (text) =>
      page.waitForFunction(
        (expected) => document.querySelector('#output').textContent.includes(expected),
        text
      );

    await go('/nodes');
    await page.locator('[data-action="status"]').click();
    await output('inactive');
    await go('/nodes/config');
    await page.locator('#readConfig').click();
    await page.waitForFunction(() => document.querySelector('#configContent').value === 'motd=after\n');
    await page.locator('#configContent').fill('motd=browser-test\n');
    await page.locator('#saveConfig').click();
    await output('restart_required');
    await go('/nodes/plugins');
    await page.locator('#plugins').click();
    await output('Smoke.jar');
    await go('/nodes/players');
    await page.locator('#players').click();
    await output('SmokePlayer');
    const uuid = '00000000-0000-0000-0000-000000000001';
    await page.locator('#playerUUID').fill(uuid);
    await page.locator('#inventory').click();
    await output('sha256');
    await page.locator('#material').fill('EMERALD');
    await page.locator('#writeItem').click();
    await output('EMERALD');

    await go('/admin/roles');
    await page.locator('#roleName').fill('support');
    await page.locator('#grants [value="nodes:read"]').check();
    await page.locator('#grants [value="players:read"]').check();
    await page.locator('#grants [value="inventory:read"]').check();
    await page.locator('#saveRole').click();
    await page.waitForFunction(() => document.querySelector('#selectedRole').value === 'support');
    await go('/admin/users');
    const support = { username: 'staff', password: 'browser-test-staff-passphrase' };
    await page.locator('#newUser').fill(support.username);
    await page.locator('#newRole').selectOption('support');
    await page.locator('#initialPassword').fill(support.password);
    await page.locator('#createUser button').click();
    await page.waitForFunction(() => document.querySelector('#userList').textContent.includes('staff'));
    await page.screenshot({ path: '/tmp/mcm-desktop.png', fullPage: true });
    await page.setViewportSize({ width: 390, height: 844 });
    assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth), false);
    await page.screenshot({ path: '/tmp/mcm-mobile.png', fullPage: true });
    await logout(page, base);

    await authenticate(page, base, support);
    assert.equal(await page.locator('nav a[href="/admin/users"]').count(), 0);
    assert.equal((await page.goto(base + '/admin/users')).status(), 403);
    await go('/nodes/players');
    assert.equal(await page.locator('#playerUUID').inputValue(), '');
    assert.equal(await page.locator('#writeItem').count(), 0);
    await page.locator('#players').click();
    await output('SmokePlayer');
    await logout(page, base);
    await page.goto(base + '/nodes');
    assert.equal(new URL(page.url()).pathname, '/login');
    await authenticate(page, base);
    await go('/nodes/players');
    assert.equal(await page.locator('#playerUUID').inputValue(), uuid);
    assert.deepEqual(errors, []);
    console.log(
      'PASS owner setup, node/config/plugin/inventory flows, custom role, staff login, direct page denial, isolated saved choices, logout and mobile layout'
    );
  } finally {
    await browser.close();
  }
})().catch((e) => {
  console.error(e);
  process.exitCode = 1;
});

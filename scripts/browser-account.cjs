const assert = require('node:assert/strict');

// Credentials exist only in the isolated smoke-test database.
const owner = { username: 'panel-owner', password: 'browser-test-owner-passphrase' };

async function authenticate(page, base, account = owner, bootstrapKey) {
  const entry = bootstrapKey ? '/setup' : '/login';
  await page.goto(base + entry);
  if (bootstrapKey) await page.locator('#bootstrapKey').fill(bootstrapKey);
  await page.locator('#username').fill(account.username);
  await page.locator('#password').fill(account.password);
  await Promise.all([page.waitForURL(base + '/'), page.locator('#authForm button').click()]);
  assert.equal(await page.locator('.identity a').textContent(), account.username);
}

async function logout(page, base) {
  await Promise.all([page.waitForURL(base + '/login'), page.locator('#logout').click()]);
}

module.exports = { authenticate, logout, owner };

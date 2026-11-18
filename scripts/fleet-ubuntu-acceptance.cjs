// Runs only from fleet-ubuntu-acceptance.sh. All connections stay on this VM.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const os = require('node:os');
const { setTimeout: delay } = require('node:timers/promises');
const { chromium } = require('playwright');

async function acceptance(page, dir, server) {
  const read = name => fs.readFileSync(path.join(dir, name), 'utf8');
  const base = 'http://127.0.0.1:39080';
  const token = read('data/admin-key.txt').trim();
  const host = 'ubuntu-ci';
  const key = read('client');
  const fingerprint = read('fingerprint').trim();
  async function fleet(method, route, data) {
    const response = await page.request.fetch(base + '/v1/fleet/' + route, {
      method, data, headers: { Authorization: 'Bearer ' + token }
    });
    assert.ok(response.ok(), method + ' ' + route + ' returned ' + response.status());
    return response.json();
  }
  async function job(action, input) {
    const created = await fleet('POST', 'jobs', {host, action, input});
    const deadline = Date.now() + 45000;
    while (Date.now() < deadline) {
      const state = await fleet('GET', 'jobs/' + created.id);
      if (state.status === 'succeeded') return state.result;
      if (state.status !== 'queued' && state.status !== 'running') {
        throw Error(action + ': ' + state.status + ' ' + state.error);
      }
      await delay(250);
    }
    throw Error(action + ' timed out');
  }

  await page.goto(base + '/fleet');
  await page.locator('#key').fill(token);
  await page.locator('#connect').click();
  await page.locator('#hosts summary').click();
  await page.locator('#hostID').fill(host);
  await page.locator('#address').fill('127.0.0.1');
  await page.locator('#port').fill('39222');
  await page.locator('#user').fill(os.userInfo().username);
  await page.locator('#discover').click();
  await page.waitForFunction(fp => document.querySelector('#fingerprint').value === fp, fingerprint);
  await page.locator('#trust').check();
  await page.locator('#privateKey').fill(read('unregistered'));
  await page.locator('#addHost').click();
  await page.waitForFunction(() => document.querySelector('#message').textContent.includes('SSH authentication'));
  assert.deepEqual(await fleet('GET', 'hosts'), []);

  await page.locator('#privateKey').fill(key);
  await page.locator('#fingerprint').fill('SHA256:' + 'A'.repeat(43));
  assert.equal(await page.locator('#trust').isChecked(), false);
  await page.locator('#trust').check();
  await page.locator('#testHost').click();
  await page.waitForFunction(() => document.querySelector('#message').textContent.includes('fingerprint mismatch'));
  assert.deepEqual(await fleet('GET', 'hosts'), []);

  await page.locator('#fingerprint').fill(fingerprint);
  await page.locator('#trust').check();
  await page.locator('#testHost').click();
  await page.waitForFunction(() => document.querySelector('#output').textContent.includes('ram_total'));
  assert.deepEqual(await fleet('GET', 'hosts'), []);
  await page.locator('#addHost').click();
  await page.waitForFunction(() => document.querySelector('#metricsView').textContent.includes('GiB'));
  assert.equal(await page.locator('#privateKey').inputValue(), '');
  const hosts = await fleet('GET', 'hosts');
  assert.equal(hosts.length, 1);
  assert.equal(hosts[0].id, host);
  assert.equal(Object.hasOwn(hosts[0], 'private_key'), false);
  assert.equal(fs.readFileSync(path.join(dir, 'data/fleet.vault')).includes(key), false);
  console.log('PASS browser enrollment, host key, rejected credential, test-only and real VM metrics');

  const service = 'mcm-' + server + '.service';
  const serviceJob = verb => job('service', {service, verb, confirm: service});
  const list = await job('servers', {});
  assert.ok(list.servers.some(item => item.server === server && item.active));
  const before = await serviceJob('status');
  const backup = await job('backup', {server, retain: 2});
  assert.match(backup.sha256, /^[a-f0-9]{64}$/);
  const restarted = await serviceJob('status');
  assert.match(restarted.output, /^ActiveState=active$/m);
  const pid = result => /^MainPID=(\d+)$/m.exec(result.output)[1];
  assert.notEqual(pid(before), pid(restarted));
  await serviceJob('stop');
  const file = '/srv/minecmanager/' + server + '/world/level.dat';
  const original = await job('read', {path: file});
  await job('write', {path: file, confirm: file, sha256: original.sha256, content: Buffer.from('changed\n').toString('base64')});
  const restored = await job('restore', {server, backup: backup.backup, confirm: server});
  const recovered = await job('read', {path: file});
  const previous = await job('read', {path: restored.previous_root + '/world/level.dat'});
  assert.equal(recovered.sha256, original.sha256);
  assert.equal(Buffer.from(recovered.content, 'base64').toString(), 'original world\n');
  assert.equal(Buffer.from(previous.content, 'base64').toString(), 'changed\n');
  assert.match((await serviceJob('status')).output, /^ActiveState=inactive$/m);

  const events = read('data/audit.jsonl').trim().split('\n').map(line => JSON.parse(line));
  assert.ok(events.some(e => e.node_id === host && e.action === 'POST /v1/fleet/hosts' && e.phase === 'result' && e.status === 502));
  assert.ok(events.some(e => e.node_id === host && e.key_id === 'bootstrap' && e.action.endsWith(':restore') && e.phase === 'result' && e.status === 200));
  console.log('PASS actual systemd stop/resume, snapshot, edited world, verified restore, previous root, stopped service and audit');
}

(async () => {
  const browser = await chromium.launch();
  try { await acceptance(await browser.newPage(), process.argv[2], process.argv[3]); }
  finally { await browser.close(); }
})().catch(err => { console.error(err.message); process.exitCode = 1; });

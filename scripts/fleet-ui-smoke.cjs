const {chromium}=require('playwright');
(async()=>{
 const browser=await chromium.launch({headless:true,...(process.env.MCM_CHROMIUM?{executablePath:process.env.MCM_CHROMIUM}:{})});
 try{
  const page=await browser.newPage({viewport:{width:1280,height:900}}),errors=[];page.on('pageerror',e=>errors.push(e.message));page.on('dialog',d=>d.accept());
  let patchPhase = 'installed', patchChecked = false, approvals = 0;
  await page.route('**/v1/fleet/update-targets', route => route.fulfill({json:['survival']}));
  await page.route('**/v1/fleet/updates*', async route => {
    const request = route.request();
    const node = new URL(request.url()).searchParams.get('node');
    const input = request.postDataJSON();
    if (input) {
      if (node !== 'survival') throw Error('Update was sent to the wrong target');
      if (input.action === 'check') patchChecked = true;
      else if (input.action === 'apply' && input.version === 'v1.1.0') { ++approvals; patchPhase = 'queued'; }
      else throw Error('Unexpected update approval');
    }
    const done = patchPhase === 'queued' && !input;
    if (done) patchPhase = 'succeeded';
    await route.fulfill({status:input?.action === 'apply' ? 202 : 200, json:{
      installed:done ? 'v1.1.0' : 'v1.0.0', latest:patchChecked ? 'v1.1.0' : 'v1.0.0',
      available:patchChecked && patchPhase === 'installed', phase:patchPhase
    }});
  });
  await page.goto(process.env.MCM_FLEET_URL+'/fleet');await page.locator('#key').fill(process.env.MCM_UI_KEY);await page.locator('#connect').click();await page.waitForFunction(()=>document.querySelector('#message').textContent==='สำเร็จ');
  await page.locator('#updateTarget').selectOption('survival');
  await page.waitForFunction(() => document.querySelector('#releaseInfo').textContent.includes('v1.0.0'));
  if (await page.locator('#applyRelease').isEnabled()) throw Error('Up-to-date install allowed update');
  await page.locator('#checkRelease').click();
  await page.locator('#applyRelease').click();
  await page.waitForFunction(() => document.querySelector('#releaseInfo').textContent.includes('อัปเดตสำเร็จ'));
  if (approvals !== 1 || await page.locator('#applyRelease').isEnabled()) throw Error('Duplicate or stale release approval');
  await page.locator('#hosts summary').click();await page.locator('#hostID').fill('game');await page.locator('#address').fill('192.0.2.1');await page.locator('#fingerprint').fill('SHA256:'+'A'.repeat(43));await page.locator('#trust').check();await page.locator('#password').fill('secret-password');await page.locator('#addHost').click();await page.waitForFunction(()=>document.querySelector('#metricsView').textContent.includes('4 cores'));
  if(await page.locator('#password').inputValue())throw Error('SSH password not cleared');
  await page.locator('#version').fill('1.21.11');await page.locator('#eula').check();await page.locator('#deploy').click();await page.waitForFunction(()=>document.querySelector('#jobRows').textContent.includes('deploy'));await page.waitForFunction(()=>document.querySelector('#jobRows').textContent.includes('succeeded'));
  await page.locator('#path').fill('/srv/minecmanager/survival/server.properties');await page.locator('#readFile').click();await page.waitForFunction(()=>document.querySelector('#fileContent').value==='motd=before\n');await page.locator('#fileContent').fill('motd=browser\n');await page.locator('#saveFile').click();await page.waitForFunction(()=>document.querySelector('#output').textContent.includes('previous_copy'));
  await page.locator('#backupNow').click();await page.waitForFunction(()=>document.querySelector('#jobRows').textContent.includes('backup'));await page.locator('#saveSchedule').click();await page.waitForFunction(()=>document.querySelector('#output').textContent.includes('every_hours'));
  await page.screenshot({path:'/tmp/mcm-fleet-desktop.png',fullPage:true});await page.setViewportSize({width:390,height:844});if(await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth))throw Error('Mobile overflow');await page.screenshot({path:'/tmp/mcm-fleet-mobile.png',fullPage:true});
  await page.locator('#logout').click();if(await page.locator('#key').inputValue())throw Error('Key not cleared');if(errors.length)throw Error(errors.join('\n'));console.log('PASS fleet UI: host creation, metrics, deploy, file editing, backup, schedules, logout and mobile layout (remote runner mocked)');
 }finally{await browser.close()}
})().catch(e=>{console.error(e);process.exit(1)});

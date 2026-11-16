const {chromium}=require('playwright');
(async()=>{
 const browser=await chromium.launch({headless:true,...(process.env.MCM_CHROMIUM?{executablePath:process.env.MCM_CHROMIUM}:{})});
 try{
  const page=await browser.newPage({viewport:{width:1280,height:900}}),errors=[];page.on('pageerror',e=>errors.push(e.message));page.on('dialog',d=>d.accept());
  await page.goto(process.env.MCM_FLEET_URL+'/fleet');await page.locator('#key').fill(process.env.MCM_UI_KEY);await page.locator('#connect').click();await page.waitForFunction(()=>document.querySelector('#message').textContent==='สำเร็จ');
  await page.locator('#hosts summary').click();await page.locator('#hostID').fill('game');await page.locator('#address').fill('192.0.2.1');await page.locator('#fingerprint').fill('SHA256:'+'A'.repeat(43));await page.locator('#trust').check();await page.locator('#password').fill('secret-password');await page.locator('#addHost').click();await page.waitForFunction(()=>document.querySelector('#metricsView').textContent.includes('4 cores'));
  if(await page.locator('#password').inputValue())throw Error('SSH password not cleared');
  await page.locator('#version').fill('1.21.11');await page.locator('#eula').check();await page.locator('#deploy').click();await page.waitForFunction(()=>document.querySelector('#jobRows').textContent.includes('deploy'));await page.waitForFunction(()=>document.querySelector('#jobRows').textContent.includes('succeeded'));
  await page.locator('#path').fill('/srv/minecmanager/survival/server.properties');await page.locator('#readFile').click();await page.waitForFunction(()=>document.querySelector('#fileContent').value==='motd=before\n');await page.locator('#fileContent').fill('motd=browser\n');await page.locator('#saveFile').click();await page.waitForFunction(()=>document.querySelector('#output').textContent.includes('previous_copy'));
  await page.locator('#backupNow').click();await page.waitForFunction(()=>document.querySelector('#jobRows').textContent.includes('backup'));await page.locator('#saveSchedule').click();await page.waitForFunction(()=>document.querySelector('#output').textContent.includes('every_hours'));
  await page.screenshot({path:'/tmp/mcm-fleet-desktop.png',fullPage:true});await page.setViewportSize({width:390,height:844});if(await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth))throw Error('Mobile overflow');await page.screenshot({path:'/tmp/mcm-fleet-mobile.png',fullPage:true});
  await page.locator('#logout').click();if(await page.locator('#key').inputValue())throw Error('Key not cleared');if(errors.length)throw Error(errors.join('\n'));console.log('PASS fleet UI: host creation, metrics, deploy, file editing, backup, schedules, logout and mobile layout (remote runner mocked)');
 }finally{await browser.close()}
})().catch(e=>{console.error(e);process.exit(1)});

const {chromium}=require('playwright');
(async()=>{
 const browser=await chromium.launch({headless:true,...(process.env.MCM_CHROMIUM?{executablePath:process.env.MCM_CHROMIUM}:{})});
 try{
  const page=await browser.newPage({viewport:{width:1280,height:900}});const errors=[];page.on('pageerror',e=>errors.push(e.message));
  await page.goto('http://127.0.0.1:9080/');await page.locator('#key').fill(process.env.MCM_UI_KEY);await page.locator('#connect').click();await page.waitForFunction(()=>document.querySelector('#node').options.length===1);
  await page.locator('[data-action="status"]').click();await page.waitForFunction(()=>document.querySelector('#output').textContent.includes('inactive'));
  await page.locator('#readConfig').click();await page.waitForFunction(()=>document.querySelector('#configContent').value==='motd=after\n');
  page.on('dialog',d=>d.accept());await page.locator('#configContent').fill('motd=browser-test\n');await page.locator('#saveConfig').click();await page.waitForFunction(()=>document.querySelector('#output').textContent.includes('restart_required'));
  await page.locator('#plugins').click();await page.waitForFunction(()=>document.querySelector('#output').textContent.includes('Smoke.jar'));
  await page.locator('#players').click();await page.waitForFunction(()=>document.querySelector('#output').textContent.includes('SmokePlayer'));
  await page.locator('#playerUUID').fill('00000000-0000-0000-0000-000000000001');await page.locator('#inventory').click();await page.waitForFunction(()=>document.querySelector('#output').textContent.includes('sha256'));await page.locator('#material').fill('EMERALD');await page.locator('#writeItem').click();await page.waitForFunction(()=>document.querySelector('#output').textContent.includes('EMERALD'));
  await page.screenshot({path:'/tmp/mcm-desktop.png',fullPage:true});
  await page.setViewportSize({width:390,height:844});if(await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth))throw Error('Mobile overflow');await page.screenshot({path:'/tmp/mcm-mobile.png',fullPage:true});
  await page.locator('#logout').click();if(await page.locator('#key').inputValue())throw Error('Key not cleared');if(errors.length)throw Error(errors.join('\n'));
  console.log('PASS: browser login, node status, config read/write, plugins, Paper inventory flow, logout, responsive layout; no JS errors');
 }finally{await browser.close()}
})().catch(e=>{console.error(e);process.exit(1)});

#!/usr/bin/env node
// Built Desktop + real Go API/PostgreSQL integration. The parent Go test owns
// an isolated database, fake Kubernetes, and explicit decision/harness fixtures.
// No API routes are intercepted here. This is application proof, not a live
// classifier benchmark or production acceptance.
import assert from 'node:assert/strict';
import { mkdir } from 'node:fs/promises';
import { dirname } from 'node:path';
const origin = process.env.ANVIL_SUBJECT_API_ORIGIN;
if (!origin) throw new Error('Run through TestSubjectManagerBrowser with ANVIL_SUBJECT_BROWSER=1.');
const { chromium } = await import(process.env.PLAYWRIGHT_MODULE || 'playwright');
const browser = await chromium.launch({headless:true, ...(process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE ? {executablePath:process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE} : {})});
const context = await browser.newContext({viewport:{width:1540,height:1000}});
await context.addInitScript(() => {
  localStorage.setItem('anvil-agents-desktop.chat-location','remote');
  sessionStorage.setItem('anvil-agents-desktop.accessToken','alice');
  sessionStorage.setItem('anvil-agents-desktop.expiresAt',String(Date.now()+3600000));
});
const page = await context.newPage();
page.setDefaultTimeout(15000);
const errors=[];
page.on('pageerror',error=>errors.push(error.message));
const headers={Authorization:'Bearer alice'};
async function api(path) {
  const response=await page.request.get(`${origin}/api/v1/namespaces/agents/chat/${path}`,{headers});
  assert.equal(response.status(),200,await response.text());
  return response.json();
}
async function send(content,observeClassification=false) {
  await page.getByLabel('Message the manager').fill(content);
  await page.getByRole('button',{name:'Send ↗',exact:true}).click();
  if(observeClassification) {
    await page.getByRole('heading',{name:'Routing live',exact:true}).waitFor();
    await page.getByText('Matching subject and intent',{exact:true}).waitFor();
  }
  await page.getByRole('button',{name:'Send ↗',exact:true}).waitFor();
  await page.waitForFunction(()=>!document.querySelector('#manager-draft')?.disabled);
}
try {
  await page.goto(`${origin}/manager`);
  await page.getByRole('heading',{name:'Your work, in context.'}).waitFor();
  await send('Build a launch website',true);
  await page.getByText('Fixture harness reply: this subject turn completed.',{exact:true}).first().waitFor();
  let receipts=(await api('routing')).items;
  const first=receipts.find(item=>item.content==='Build a launch website');
  assert.equal(first.status,'accepted');
  assert.ok(first.result.turn.id);
  assert.ok(first.events.find(event=>event.step==='classifying'));
  const firstThread=first.threadId;

  await send('How is the launch website going?',true);
  receipts=(await api('routing')).items;
  const followup=receipts.find(item=>item.content==='How is the launch website going?');
  assert.equal(followup.status,'accepted');
  assert.equal(followup.threadId,firstThread);
  assert.equal(followup.events.find(event=>event.decision)?.decision.intent,'status');

  await send('Start a documentation project',true);
  let vault=(await api('subjects')).items;
  assert.equal(vault.length,2);
  const launch=vault.find(item=>item.threadId===firstThread);
  assert.ok(launch);

  await send('Which one should get this?',true);
  await page.getByText('Which subject is this about? Choose one below, or edit your message to clarify the request.',{exact:true}).waitFor();
  receipts=(await api('routing')).items;
  const uncertain=receipts.find(item=>item.content==='Which one should get this?');
  assert.equal(uncertain.status,'clarify');
  assert.equal(uncertain.turnId,undefined);
  await page.locator('.manager-clarification-options').getByRole('button',{name:launch.label,exact:true}).click();
  await page.getByRole('button',{name:'Send ↗',exact:true}).waitFor();
  await page.waitForFunction(()=>!document.querySelector('#manager-draft')?.disabled);
  receipts=(await api('routing')).items;
  const resolved=receipts.find(item=>item.id===uncertain.id);
  assert.equal(resolved.status,'accepted');
  assert.equal(resolved.threadId,firstThread);
  assert.equal(receipts.length,4,'clarification must reuse the original request');

  await page.reload();
  await page.getByText('Build a launch website',{exact:true}).first().waitFor();
  await page.locator('.manager-message').filter({hasText:'Which one should get this?'}).getByText('Handed off',{exact:true}).waitFor();
  assert.equal((await api('routing')).items.length,4);
  const screenshot=process.env.ANVIL_SUBJECT_SCREENSHOT || '/tmp/anvil-subject-manager-live.png';
  await mkdir(dirname(screenshot),{recursive:true});
  await page.screenshot({path:screenshot,fullPage:true});
  await page.setViewportSize({width:480,height:1000});
  const overflow=await page.evaluate(()=>document.documentElement.scrollWidth>window.innerWidth+1);
  assert.equal(overflow,false,'manager must fit a narrow screen without page overflow');
  assert.deepEqual(errors,[]);
  console.log(JSON.stringify({status:'passed',proof:'Built Desktop with actual Go API and durable subject receipts; explicit provider/harness fixtures',requests:4,subjects:2,reusedThread:firstThread,screenshot}));
} finally { await browser.close(); }

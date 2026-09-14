#!/usr/bin/env node
// UI contract test: isolated browser + in-memory HTTP fixture. No real OIDC,
// remote runner, or model is used. Build web/desktop first; install Playwright
// separately and set PLAYWRIGHT_MODULE to its absolute index.mjs when needed.
import assert from 'node:assert/strict';
import { createServer } from 'node:http';
import { readFile } from 'node:fs/promises';
import { fileURLToPath } from 'node:url';
import { resolve, extname } from 'node:path';
const { chromium } = await import(process.env.PLAYWRIGHT_MODULE || 'playwright');
const dist = fileURLToPath(new URL('../web/desktop/dist/', import.meta.url));
const server = createServer(async (req, res) => {
  const path = new URL(req.url, 'http://fixture').pathname;
  const asset = path.startsWith('/assets/') ? resolve(dist, `.${path}`) : resolve(dist, 'index.html');
  try {
    res.setHeader('Content-Type', ({'.js':'application/javascript','.css':'text/css','.html':'text/html'})[extname(asset)] || 'application/octet-stream');
    res.end(await readFile(asset));
  } catch { res.writeHead(404).end(); }
});
await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
const origin = `http://127.0.0.1:${server.address().port}`;
const browser = await chromium.launch({headless:true, ...(process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE ? {executablePath:process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE} : {})});
const context = await browser.newContext();
const page = await context.newPage();
await page.clock.install();
page.setDefaultTimeout(10000);
const errors = [], posts = [], creates = [], threads = new Map();
let failNext = false, loseNextReceipt = false;
const withheldDetails = new Map(), deniedDetails = new Map(), detailGets = new Map();
page.on('pageerror', error => errors.push(error.message));
await context.addInitScript(() => {
  sessionStorage.setItem('anvil-agents-desktop.accessToken', 'ui-contract-fixture-not-a-token');
  sessionStorage.setItem('anvil-agents-desktop.expiresAt', String(Date.now()+3600000));
});
const profile = name => ({apiVersion:'control.anvil.hazyforge.io/v1alpha1',kind:'AgentRunProfile',metadata:{name},spec:{harnessProfileRef:{name:'codex-standard'}}});
const harness = (name, kind) => ({kind:'AgentHarnessProfile',metadata:{name},spec:{backend:{kind}}});
const config = {defaultNamespaces:['anvilhub','hazy-trade'],oidc:{issuer:'https://fixture.invalid',clientId:'fixture-console',audiences:['fixture'],scopes:['openid']},desktop:{oidcClientId:'fixture-native'},composition:{readEnabled:true,writeEnabled:true},runs:{createEnabled:true},chat:{enabled:true}};
const reply = async (route, data, status=200) => route.fulfill({status,contentType:'application/json',body:JSON.stringify(data)});
await context.route('**/*', async route => {
  const request=route.request(), url=new URL(request.url()), path=url.pathname;
  if(url.origin!==origin) return route.abort();
  if(path==='/local/v1/snapshot') return reply(route,{productTitle:'Anvil Agents Desktop',prefs:{apiOrigin:origin},api:{origin,reachable:true},harnesses:[],wsl:{},harnessTarget:'native'});
  if(path==='/ui-config.json') return reply(route,config);
  if(!path.startsWith('/api/')) return route.continue();
  assert.ok(['Bearer ui-contract-fixture-not-a-token','Bearer ui-contract-refreshed-not-a-token'].includes(request.headers().authorization));
  assert.equal(url.searchParams.has('access_token'),false);
  const m=path.match(/^\/api\/v1\/namespaces\/([^/]+)\/(.*)$/);
  if(!m) return reply(route,{},404);
  const [,ns,tail]=m;
  if(tail==='agent-run-profiles') return reply(route,{items:[profile(ns==='anvilhub'?'agent-alpha':'agent-trade'),profile('agent-beta')]});
  if(tail==='agent-harness-profiles') return reply(route,{items:[harness('codex-standard','codex'),harness('agy-review','agy')]});
  if(tail==='chat/threads' && request.method()==='GET') return reply(route,{items:[...threads.values()].filter(t=>t.namespace===ns)});
  if(tail==='chat/threads' && request.method()==='POST') {
    const body=request.postDataJSON(); creates.push({ns,...body});
    const t={id:`thread-${threads.size+1}`,namespace:ns,...body,createdBy:'fixture',createdAt:new Date().toISOString(),updatedAt:new Date().toISOString(),messages:[],turns:[]};
    threads.set(t.id,t); return reply(route,t,201);
  }
  const tm=tail.match(/^chat\/threads\/([^/]+)(\/messages)?$/);
  if(tm) {
    const t=threads.get(tm[1]);
    if (!t || t.namespace!==ns) {
      errors.push(`Cross-namespace thread request: ${ns}/${tm[1]}`);
      return reply(route,{error:{code:'not_found',message:'Fixture thread is not in this namespace'}},404);
    }
    if(request.method()==='GET') {
      detailGets.set(t.id,(detailGets.get(t.id)||0)+1);
      if(deniedDetails.has(t.id)) return reply(route,{error:{code:'not_found',message:'Fixture access denied'}},deniedDetails.get(t.id));
      return reply(route,withheldDetails.get(t.id)||t);
    }
    const body=request.postDataJSON(); posts.push({thread:t.id,...body});
    if(failNext) {failNext=false;return reply(route,{error:{code:'temporarily_unavailable',message:'Fixture temporary transport failure'}},503);}
    if(loseNextReceipt) withheldDetails.set(t.id,structuredClone(t));
    let turn=t.turns.find(x=>x.id===body.requestId);
    let user=t.messages.find(x=>x.id===`user-${body.requestId}`);
    if(!turn) {
      user={id:`user-${body.requestId}`,threadId:t.id,role:'user',content:body.content,sequence:t.messages.length+1,createdAt:new Date().toISOString()};
      turn={id:body.requestId,requestId:body.requestId,status:'queued',runName:`fixture-run-${t.turns.length+1}`}; t.messages.push(user); t.turns.push(turn); t.activeTurn=turn;
    }
    if(loseNextReceipt) {loseNextReceipt=false;return reply(route,{error:{code:'receipt_lost',message:'Fixture accepted receipt was lost'}},503);}
    withheldDetails.delete(t.id);
    return reply(route,{thread:t,user,turn},202);
  }
  // Runner activity is ancillary; retain a deterministic closed stream.
  if(tail.includes('/stream')) return route.fulfill({status:200,contentType:'text/event-stream',body:'event: done\ndata: {}\n\n'});
  return reply(route,{},404);
});
const visible = async text => page.getByText(text,{exact:true}).waitFor({state:'visible'});
const complete = (id,text,failed=false) => {
  const t=threads.get(id); const turn=t.activeTurn;
  turn.status=failed?'failed':'succeeded'; if(failed)turn.error=text;
  else t.messages.push({id:`assistant-${turn.id}`,threadId:id,role:'assistant',content:text,sequence:t.messages.length+1,createdAt:new Date().toISOString()});
  delete t.activeTurn;
};
try {
  await page.goto(`${origin}/chat`);
  await page.getByLabel('Agent',{exact:true}).selectOption('agent-alpha');
  await page.getByLabel('Remote harness').selectOption('agy-review');
  await page.getByLabel('Role',{exact:true}).selectOption('fleet');
  await page.getByLabel('Allow this agent to delegate messages').check();
  await page.getByLabel('Message',{exact:true}).fill('Coordinate the fixture agents');
  assert.equal(await page.getByRole('button',{name:'Send',exact:true}).isDisabled(),true);
  await page.getByLabel('agent-beta',{exact:true}).check();
  await page.getByLabel('Message',{exact:true}).fill('Coordinate the fixture agents');
  await page.getByRole('button',{name:'Send',exact:true}).click();
  await page.getByRole('status').filter({hasText:'Queued for the remote harness'}).waitFor();
  assert.deepEqual(creates[0],{ns:'anvilhub',profileName:'agent-alpha',harnessProfileName:'agy-review',mode:'fleet',title:'Manager · agent-alpha',metadata:{coordination:{enabled:true,allowedProfiles:['agent-beta']}}});
  assert.equal(await page.getByRole('button',{name:'Waiting for reply…'}).isDisabled(),true);
  complete('thread-1','Fixture coordination response');
  await visible('Fixture coordination response');
  await page.reload();
  await page.getByRole('button',{name:/Manager · agent-alpha/}).click();
  await visible('Fixture coordination response');
  assert.equal(await page.getByLabel('Remote harness').inputValue(),'agy-review');
  assert.equal(await page.getByLabel('Role',{exact:true}).inputValue(),'fleet');
  assert.equal(await page.getByLabel('Allow this agent to delegate messages').isChecked(),true);
  assert.equal(await page.getByLabel('agent-beta',{exact:true}).isChecked(),true);
  console.log('PASS manager/agent/harness selection; asynchronous reply; persisted history reload');
  await page.evaluate(()=>sessionStorage.setItem('anvil-agents-desktop.accessToken','ui-contract-refreshed-not-a-token'));
  const refreshedRequest=page.waitForRequest(r=>r.url().includes('/chat/threads/thread-1') && r.headers().authorization==='Bearer ui-contract-refreshed-not-a-token');
  await page.clock.fastForward(60001);
  await refreshedRequest;
  await visible('Fixture coordination response');
  assert.equal(await page.getByLabel('Agent',{exact:true}).isDisabled(),true);
  assert.equal(await page.getByLabel('Agent',{exact:true}).inputValue(),'agent-alpha');
  console.log('PASS token refresh preserves selected conversation and history');

  await page.getByRole('button',{name:'New conversation'}).click();
  await page.getByLabel('Allow this agent to delegate messages').uncheck();
  await page.getByLabel('Agent',{exact:true}).selectOption('agent-beta');
  await page.getByLabel('Remote harness').selectOption('');
  await page.getByLabel('Role',{exact:true}).selectOption('persona');
  await page.getByLabel('Message',{exact:true}).fill('Retry this fixture message');
  failNext=true;
  await page.getByRole('button',{name:'Send',exact:true}).click();
  await page.getByRole('alert').filter({hasText:'Fixture temporary transport failure'}).waitFor();
  assert.equal(await page.getByLabel('Message',{exact:true}).inputValue(),'Retry this fixture message');
  await page.getByRole('button',{name:'Send',exact:true}).click();
  await page.getByRole('status').filter({hasText:'Queued for the remote harness'}).waitFor();
  assert.equal(posts.at(-1).requestId,posts.at(-2).requestId);
  assert.equal(threads.size,2); assert.equal(threads.get('thread-2').messages.length,1);
  complete('thread-2','Fixture runner failed',true);
  await visible('Turn failed: Fixture runner failed');
  await page.getByLabel('Message',{exact:true}).fill('Continue after runner failure');
  await page.getByRole('button',{name:'Send',exact:true}).click();
  await page.getByRole('status').filter({hasText:'Queued for the remote harness'}).waitFor();
  assert.notEqual(posts.at(-1).requestId,posts.at(-2).requestId);
  complete('thread-2','Fixture second conversation response');
  await visible('Fixture second conversation response');
  console.log('PASS transient failure retry reuses idempotency key; runner failure accepts a new turn');
  const priorMessages=threads.get('thread-2').messages.length;
  await page.getByLabel('Message',{exact:true}).fill('Ambiguous accepted fixture message');
  loseNextReceipt=true;
  await page.getByRole('button',{name:'Send',exact:true}).click();
  await page.getByRole('alert').filter({hasText:'Fixture accepted receipt was lost'}).waitFor();
  const ambiguousID=posts.at(-1).requestId;
  assert.equal(threads.get('thread-2').messages.length,priorMessages+1);
  await page.getByRole('button',{name:/Manager · agent-alpha/}).click();
  await visible('Fixture coordination response');
  await page.getByRole('button',{name:'agent-beta agent-beta',exact:true}).click();
  assert.equal(await page.getByLabel('Message',{exact:true}).inputValue(),'Ambiguous accepted fixture message');
  await page.reload();
  await page.getByRole('button',{name:'agent-beta agent-beta',exact:true}).click();
  assert.equal(await page.getByLabel('Message',{exact:true}).inputValue(),'Ambiguous accepted fixture message');
  await page.getByRole('button',{name:'Send',exact:true}).click();
  await page.getByRole('status').filter({hasText:'Queued for the remote harness'}).waitFor();
  assert.equal(posts.at(-1).requestId,ambiguousID);
  assert.equal(threads.get('thread-2').messages.length,priorMessages+1);
  assert.equal(threads.size,2);
  complete('thread-2','Fixture ambiguous receipt recovered');
  await visible('Fixture ambiguous receipt recovered');
  console.log('PASS lost accepted receipt retains draft and idempotency through sidebar and reload');

  await page.getByRole('button',{name:/Manager · agent-alpha/}).click();
  await visible('Fixture coordination response');
  assert.equal(await page.getByText('Fixture second conversation response',{exact:true}).count(),0);
  await page.getByRole('button',{name:'agent-beta agent-beta',exact:true}).click();
  await visible('Fixture second conversation response');
  assert.equal(await page.getByText('Fixture coordination response',{exact:true}).count(),0);
  await page.getByLabel('Namespace',{exact:true}).selectOption('hazy-trade');
  await page.getByLabel('Agent',{exact:true}).selectOption('agent-trade');
  assert.equal(await page.getByText('Fixture second conversation response',{exact:true}).count(),0);
  await page.getByLabel('Message',{exact:true}).fill('Namespace isolated fixture');
  await page.getByRole('button',{name:'Send',exact:true}).click();
  await page.getByRole('status').filter({hasText:'Queued for the remote harness'}).waitFor();
  assert.equal(creates.at(-1).ns,'hazy-trade');
  assert.deepEqual(errors,[]);
  console.log('PASS two-thread isolation and namespace switch; no browser runtime errors');
  await page.getByRole('button',{name:'New conversation'}).click();
  await page.getByLabel('Agent',{exact:true}).selectOption('');
  await page.getByLabel('Remote harness').selectOption('codex-standard');
  await page.getByLabel('Message',{exact:true}).fill('Harness-only fixture');
  await page.getByRole('button',{name:'Send',exact:true}).click();
  await page.getByRole('status').filter({hasText:'Queued for the remote harness'}).waitFor();
  assert.equal(creates.at(-1).profileName,undefined);
  assert.equal(creates.at(-1).harnessProfileName,'codex-standard');
  console.log('PASS standalone harness conversation; explicit manager peer selection survives reload');
  complete('thread-4','Fixture harness-only response');
  await visible('Fixture harness-only response');
  deniedDetails.set('thread-4',404);
  await page.getByRole('alert').filter({hasText:'Fixture access denied'}).waitFor();
  const deniedCount=detailGets.get('thread-4');
  await page.clock.fastForward(10001);
  assert.equal(detailGets.get('thread-4'),deniedCount);
  console.log('PASS denied404 stops polling until selection changes');
  config.chat.enabled=false;
  await page.reload();
  await visible('Remote chat is not enabled on this server.');
  assert.equal(await page.getByLabel('Message',{exact:true}).isDisabled(),true);
  assert.equal(await page.getByRole('button',{name:'Send',exact:true}).isDisabled(),true);
  console.log('PASS server-disabled chat cannot submit');
  console.log('UI CONTRACT TESTS PASSED — fixture responses are not remote model evidence');
} finally {await context.close();await browser.close();await new Promise(resolve=>server.close(resolve));}

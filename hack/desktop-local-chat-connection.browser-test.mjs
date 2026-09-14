#!/usr/bin/env node
// Isolated browser proof: fake OIDC session and tool bridge, no model/API calls.
import assert from 'node:assert/strict';
import {createServer} from 'node:http';
import {readFile} from 'node:fs/promises';
import {fileURLToPath} from 'node:url';
import {resolve,extname} from 'node:path';
const {chromium}=await import(process.env.PLAYWRIGHT_MODULE || 'playwright');
const dist=fileURLToPath(new URL('../web/desktop/dist/',import.meta.url));
const posts=[],errors=[];let start,finish,requestReady;let configAvailable=true;
let arrived=new Promise(r=>requestReady=r);
const server=createServer(async(req,res)=>{
 const url=new URL(req.url,'http://fixture');
 if(url.pathname==='/local/v1/snapshot') {
  assert.equal(req.headers.authorization,undefined);
  res.setHeader('Content-Type','application/json');
  res.end(JSON.stringify({productTitle:'Anvil Agents Desktop',prefs:{apiOrigin:'https://api.fixture.invalid',harnessTarget:'wsl'},api:{origin:'https://api.fixture.invalid',reachable:true},harnessTarget:'wsl',wsl:{insideWSL:true,available:true,defaultDistro:'Ubuntu'},harnesses:[{id:'prime',displayName:'Prime Agent',kind:'harness',present:true,delegatable:true,binaries:['prime-agent']}]}));return;
 }
 if(url.pathname==='/ui-config.json') {
  if(!configAvailable){res.writeHead(503).end();return;}
  assert.equal(req.headers.authorization,undefined);
  res.setHeader('Content-Type','application/json');res.end(JSON.stringify({defaultNamespaces:['other','anvilhub'],oidc:{issuer:'https://issuer.fixture.invalid',clientId:'fixture'},composition:{readEnabled:true,writeEnabled:true},runs:{createEnabled:true}}));return;
 }
 if(url.pathname==='/local/v1/chat/stream') {
  let body='';for await(const chunk of req)body+=chunk;
  posts.push({body:JSON.parse(body),authorization:req.headers.authorization});
  res.writeHead(200,{'Content-Type':'text/event-stream'});res.flushHeaders();
  const emit=(event,data)=>res.write(`event: ${event}\ndata: ${JSON.stringify(data)}\n\n`);
  start=()=>{emit('started',{anvilConnected:true,remoteNamespace:'anvilhub',target:'wsl',wslDistro:'Ubuntu',workdir:'/home/fixture/work'});emit('anvil_tool',{action:'list_agents',status:'running'});};
  finish=()=>{emit('result',{exitCode:0,stdout:JSON.stringify({type:'message_end',message:{role:'assistant',stopReason:'stop',content:[{type:'text',text:`Answer ${posts.length}`}]}})});res.end();};
  requestReady();return;
 }
 if(url.pathname.startsWith('/api/')||url.pathname.startsWith('/v1/')) {errors.push('Unexpected browser remote tool request');res.writeHead(404).end();return;}
 const path=url.pathname.startsWith('/assets/')?resolve(dist,`.${url.pathname}`):resolve(dist,'index.html');
 try{res.setHeader('Content-Type',({'.js':'application/javascript','.css':'text/css','.html':'text/html'})[extname(path)]||'application/octet-stream');res.end(await readFile(path));}catch{res.writeHead(404).end();}
});
await new Promise(r=>server.listen(0,'127.0.0.1',r));
const browser=await chromium.launch({headless:true});const page=await browser.newPage();
page.on('pageerror',e=>errors.push(e.message));
await page.addInitScript(()=>{
 localStorage.setItem('anvil-agents-desktop.chat-location','local');
 sessionStorage.setItem('anvil-agents-desktop.accessToken','fixture-old');
 sessionStorage.setItem('anvil-agents-desktop.expiresAt',String(Date.now()+3600000));
});
try {
 await page.goto(`http://127.0.0.1:${server.address().port}/chat`);
 await page.getByRole('button',{name:'Local',exact:true}).waitFor();
 await page.getByText('Anvil tools enabled',{exact:true}).waitFor();
 assert.equal(await page.getByText('Anvil connected',{exact:true}).count(),0);
 await page.locator('details.agent-settings summary').click();
 await page.getByRole('combobox',{name:'Anvil namespace',exact:true}).waitFor();
 assert.equal(await page.getByRole('combobox',{name:'Anvil namespace'}).inputValue(),'anvilhub');
 assert.match(await page.locator('details.agent-settings').innerText(),/✓ WSL · Ubuntu/);
 await page.evaluate(()=>sessionStorage.setItem('anvil-agents-desktop.accessToken','fixture-fresh'));
 await page.getByRole('textbox',{name:'Message',exact:true}).fill('List my agents');
 await page.getByRole('button',{name:'Send',exact:true}).click();await arrived;
 assert.equal(posts[0].authorization,'Bearer fixture-fresh');assert.equal(posts[0].body.remoteNamespace,'anvilhub');
 assert.equal(JSON.stringify(posts[0].body).includes('fixture-fresh'),false);
 assert.equal(await page.getByText('Anvil connected',{exact:true}).count(),0);
 start();await page.getByText('Anvil connected',{exact:true}).waitFor();await page.getByRole('status').filter({hasText:'Listing agents'}).waitFor();
 finish();await page.getByText('Answer 1',{exact:true}).waitFor();await page.getByText('Anvil tools enabled',{exact:true}).waitFor();
 await page.getByRole('checkbox',{name:'Use Anvil tools',exact:true}).uncheck();
 await page.getByText('Anvil tools off',{exact:true}).waitFor();
 arrived=new Promise(r=>requestReady=r);
 await page.getByRole('textbox',{name:'Message',exact:true}).fill('Only local work');
 await page.getByRole('button',{name:'Send',exact:true}).click();await arrived;
 assert.equal(posts[1].authorization,undefined);assert.equal(posts[1].body.remoteNamespace,undefined);
 start();assert.equal(await page.getByText('Anvil connected',{exact:true}).count(),0);
 finish();await page.getByText('Answer 2',{exact:true}).waitFor();
 assert.equal(await page.evaluate(()=>localStorage.getItem('anvil-agents-desktop.local-chat.v1').includes('fixture-fresh')),false);
 await page.getByRole('checkbox',{name:'Use Anvil tools',exact:true}).check();
 await page.evaluate(()=>sessionStorage.removeItem('anvil-agents-desktop.accessToken'));
 await page.getByRole('textbox',{name:'Message',exact:true}).fill('Keep this unsent draft');
 await page.getByRole('button',{name:'Send',exact:true}).click();
 await page.getByRole('alert').filter({hasText:'Your Anvil sign-in expired'}).waitFor();
 assert.equal(posts.length,2);
 assert.equal(await page.getByRole('textbox',{name:'Message',exact:true}).inputValue(),'Keep this unsent draft');
 const saved=await page.evaluate(()=>JSON.parse(localStorage.getItem('anvil-agents-desktop.local-chat.v1'))[0]);
 assert.equal(saved.messages.length,4);assert.equal(saved.pending,false);
 await page.getByText('Sign in to Anvil',{exact:true}).waitFor();
 configAvailable=false;await page.reload();
 await page.getByText('Anvil unavailable',{exact:true}).waitFor();
 await page.getByRole('button',{name:'Send',exact:true}).click();
 await page.getByRole('alert').filter({hasText:'Anvil tools are enabled, but the Anvil connection is unavailable or still loading'}).waitFor();
 assert.equal(posts.length,2);
 assert.equal(await page.getByRole('textbox',{name:'Message',exact:true}).inputValue(),'Keep this unsent draft');
 const unavailableSaved=await page.evaluate(()=>JSON.parse(localStorage.getItem('anvil-agents-desktop.local-chat.v1'))[0]);
 assert.equal(unavailableSaved.messages.length,4);assert.equal(unavailableSaved.pending,false);
 assert.deepEqual(errors,[]);
 console.log('PASS Local label, settings WSL status, optional per-turn Anvil bridge, fresh wrapper-only auth, enabled/confirmed/off/unavailable badges, no silent fallback, local-only mode');
} finally {await browser.close();server.closeAllConnections();await new Promise(r=>server.close(r));}

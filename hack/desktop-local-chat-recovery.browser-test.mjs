#!/usr/bin/env node
// Local UI recovery proof with an isolated HTTP fixture; no harness/model calls.
import assert from 'node:assert/strict';
import {createServer} from 'node:http';
import {readFile} from 'node:fs/promises';
import {fileURLToPath} from 'node:url';
import {resolve,extname} from 'node:path';
const {chromium}=await import(process.env.PLAYWRIGHT_MODULE || 'playwright');
const dist=fileURLToPath(new URL('../web/desktop/dist/',import.meta.url));
const posts=[],errors=[];let distro='Ubuntu';
const reply=text=>JSON.stringify({type:'message_end',message:{role:'assistant',stopReason:'stop',content:[{type:'text',text}]}});
const server=createServer(async(req,res)=>{
  const url=new URL(req.url,'http://fixture');
  if(url.pathname==='/local/v1/snapshot') {
    res.setHeader('Content-Type','application/json');
    res.end(JSON.stringify({productTitle:'Anvil Agents Desktop',prefs:{harnessTarget:'wsl'},api:{reachable:false},harnessTarget:'wsl',wsl:{insideWSL:true,available:true,defaultDistro:distro},harnesses:[{id:'prime',displayName:'Prime Agent',kind:'harness',present:true,delegatable:true,binaries:['prime-agent']}]}));return;
  }
  if(url.pathname==='/local/v1/chat/stream') {
    assert.equal(req.headers.authorization,undefined);
    let body='';for await(const chunk of req)body+=chunk;posts.push(JSON.parse(body));
    res.writeHead(200,{'Content-Type':'text/event-stream'});
    const emit=(name,data)=>res.write(`event: ${name}\ndata: ${JSON.stringify(data)}\n\n`);
    emit('started',{target:'wsl',wslDistro:distro,workdir:posts.at(-1).workdir});
    if(posts.length===1) {setTimeout(()=>res.destroy(),100);return;}
    emit('stdout',{line:reply('Earlier completion')});
    emit('result',{exitCode:0,stdout:reply('Earlier completion')+'\n'+reply('Recovered final answer')});res.end();return;
  }
  if(url.pathname.startsWith('/api/')||url.pathname==='/ui-config.json') {errors.push('Unexpected remote API');res.writeHead(404).end();return;}
  const path=url.pathname.startsWith('/assets/')?resolve(dist,`.${url.pathname}`):resolve(dist,'index.html');
  try {res.setHeader('Content-Type',({'.js':'application/javascript','.css':'text/css','.html':'text/html'})[extname(path)]||'application/octet-stream');res.end(await readFile(path));} catch {res.writeHead(404).end();}
});
await new Promise(r=>server.listen(0,'127.0.0.1',r));
const browser=await chromium.launch({headless:true});const page=await browser.newPage();
page.on('pageerror',e=>errors.push(e.message));
await page.addInitScript(()=>{
  const key='anvil-agents-desktop.local-chat.v1';
  localStorage.setItem('anvil-agents-desktop.chat-location','local');
  if(!localStorage.getItem(key)) {
    localStorage.setItem(key,JSON.stringify([{id:'pending-proof',title:'Restored work',harness:'prime',workdir:'/home/fixture/work',target:'wsl',wslDistro:'Ubuntu',messages:[{role:'user',content:'Earlier task'}],draft:'Next task',pending:true}]));
    localStorage.setItem(`${key}.selected`,'pending-proof');
  }
});
try {
  await page.goto(`http://127.0.0.1:${server.address().port}/chat`);
  const send=page.getByRole('button',{name:'Send',exact:true});
  const ack=()=>page.getByRole('button',{name:'I checked that this work has stopped',exact:true});
  await ack().waitFor();assert.equal(await send.isDisabled(),true);
  await page.getByRole('button',{name:'New agent',exact:true}).click();
  await page.getByRole('textbox',{name:'Agent name',exact:true}).fill('Research partner');
  await page.getByRole('button',{name:'Create agent',exact:true}).click();
  await page.getByRole('textbox',{name:'Message',exact:true}).fill('Would duplicate work');
  assert.equal(await send.isDisabled(),true);assert.equal(posts.length,0);
  await page.getByRole('button',{name:'Open Prime Agent',exact:true}).click();
  const migrated = await page.evaluate(()=>JSON.parse(localStorage.getItem('anvil-agents-desktop.local-chat.v1')).find(c=>c.id==='pending-proof'));
  assert.equal(migrated.name,'Prime Agent');assert.equal(migrated.title,'Restored work');assert.equal(migrated.draft,'Next task');assert.equal(migrated.messages[0].content,'Earlier task');
  await ack().click();await send.click();await ack().waitFor();
  assert.equal(await send.isDisabled(),true);assert.equal(posts.length,1);
  await page.reload();await ack().waitFor();assert.equal(posts.length,1);
  await ack().click();distro='Debian';await page.reload();
  const bind=page.getByRole('button',{name:'Use this folder on WSL · Debian',exact:true});
  await bind.waitFor();
  await page.getByRole('textbox',{name:'Message',exact:true}).fill('Continue in the checked folder');
  assert.equal(await send.isDisabled(),true);
  await page.getByRole('textbox',{name:'Working folder',exact:true}).fill('/home/fixture/new project');
  await bind.click();await send.click();
  await page.getByText('Recovered final answer',{exact:true}).waitFor();
  assert.equal(await page.getByText('Earlier completion',{exact:true}).count(),0);
  assert.equal(posts.length,2);assert.equal(posts[1].workdir,'/home/fixture/new project');
  assert.deepEqual(errors,[]);
  console.log('PASS restored/transport-uncertain turn gate, explicit acknowledgement, target binding/folder repair, unterminated final reply');
} finally {await browser.close();server.closeAllConnections();await new Promise(r=>server.close(r));}

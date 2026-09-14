#!/usr/bin/env node
// Isolated local-chat UI contract; no installed harness, login, or model call.
import assert from 'node:assert/strict';
import {createServer} from 'node:http';
import {readFile} from 'node:fs/promises';
import {fileURLToPath} from 'node:url';
import {resolve, extname} from 'node:path';
const {chromium} = await import(process.env.PLAYWRIGHT_MODULE || 'playwright');
const dist=fileURLToPath(new URL('../web/desktop/dist/',import.meta.url));
const posts=[],errors=[];let finish;
const server=createServer(async(req,res)=>{
  const url=new URL(req.url,'http://fixture');
  if(url.pathname==='/local/v1/snapshot') {res.setHeader('Content-Type','application/json');res.end(JSON.stringify({productTitle:'Anvil Agents Desktop',prefs:{harnessTarget:'wsl'},api:{reachable:false},harnessTarget:'wsl',wsl:{insideWSL:true,available:true,defaultDistro:'Ubuntu'},harnesses:[{id:'prime',displayName:'Prime Agent',kind:'harness',present:true,delegatable:true,binaries:['prime-agent']}]}));return;}
  if(url.pathname==='/local/v1/chat/stream') {
    assert.equal(req.headers.authorization,undefined);
    let body='';for await(const chunk of req)body+=chunk;
    posts.push(JSON.parse(body));
    res.writeHead(200,{'Content-Type':'text/event-stream'});
    const event=(name,data)=>res.write(`event: ${name}\ndata: ${JSON.stringify(data)}\n\n`);
    event('started',{harness:'prime',target:'wsl',workdir:'/home/fixture/Anvil workspace'});
    event('stdout',{line:JSON.stringify({type:'tool_execution_start',toolName:'ipython',toolCallId:'tool1',args:{code:'PRIVATE_COMMAND'}})});
    event('stdout',{line:JSON.stringify({type:'message_update',assistantMessageEvent:{type:'thinking_delta',delta:'PRIVATE_REASONING'}})});
    finish=(failure=false)=>{const line=JSON.stringify({type:'message_end',message:{role:'assistant',stopReason:'stop',content:[{type:'thinking',thinking:'PRIVATE_REASONING'},{type:'text',text:'Fixture verified result'}]}});event('stdout',{line});event('result',{exitCode:failure?1:0,stdout:line,stderr:'PRIVATE_STDERR'});res.end();};
    return;
  }
  if(url.pathname.startsWith('/api/')||url.pathname==='/ui-config.json'){errors.push(`Unexpected remote request ${url.pathname}`);res.writeHead(404).end();return;}
  const path=url.pathname.startsWith('/assets/')?resolve(dist,`.${url.pathname}`):resolve(dist,'index.html');
  try{res.setHeader('Content-Type',({'.js':'application/javascript','.css':'text/css','.html':'text/html'})[extname(path)]||'application/octet-stream');res.end(await readFile(path));}catch{res.writeHead(404).end();}
});
await new Promise(r=>server.listen(0,'127.0.0.1',r));
const browser=await chromium.launch({headless:true});const page=await browser.newPage();
page.on('pageerror',e=>errors.push(e.message));
await page.addInitScript(()=>localStorage.setItem('anvil-agents-desktop.chat-location','local'));
try {
 await page.goto(`http://127.0.0.1:${server.address().port}/chat`);
 await page.getByRole('combobox',{name:'Local harness'}).waitFor();
 assert.equal(await page.getByRole('combobox',{name:'Local harness'}).inputValue(),'prime');
 await page.getByRole('textbox',{name:'Message',exact:true}).fill('Write and verify a plan');
 await page.getByRole('button',{name:'Send',exact:true}).click();
 await page.getByRole('region',{name:'Local agent activity'}).getByText(/Python|ipython/i).first().waitFor();
 assert.equal(posts.length,1);assert.equal(posts[0].harness,'prime');
 assert.equal(await page.getByRole('button',{name:'Primaris',exact:true}).isDisabled(),true);
 assert.equal((await page.locator('body').innerText()).includes('PRIVATE_'),false);
 finish();await page.getByText('Fixture verified result',{exact:true}).waitFor();
 assert.equal(await page.getByRole('textbox',{name:'Working folder'}).inputValue(),'/home/fixture/Anvil workspace');
 await page.getByRole('textbox',{name:'Message',exact:true}).fill('Follow-up draft');await page.reload();
 await page.getByText('Fixture verified result',{exact:true}).waitFor();
 assert.equal(await page.getByRole('textbox',{name:'Message',exact:true}).inputValue(),'Follow-up draft');
 await page.getByRole('button',{name:'Send',exact:true}).click();
 await page.getByRole('region',{name:'Local agent activity'}).getByText(/Python|ipython/i).first().waitFor();
 assert.equal(posts.length,2);assert.ok(posts[1].prompt.includes('Fixture verified result'));assert.equal(posts[1].workdir,'/home/fixture/Anvil workspace');
 finish(true);await page.getByRole('alert').waitFor();
 assert.equal(await page.getByText('Fixture verified result',{exact:true}).count(),1);
 assert.equal((await page.locator('body').innerText()).includes('PRIVATE_'),false);
 assert.deepEqual(errors,[]);
 console.log('PASS local Prime default, no remote auth/API, live public activity, history/folder/draft reload, failed turn without fake reply');
}finally{await browser.close();server.closeAllConnections();await new Promise(r=>server.close(r));}

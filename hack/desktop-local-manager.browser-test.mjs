#!/usr/bin/env node
// Routing presentation owner: streamed classifier stages, stale selection,
// manual routing and feedback export. Actual-Go browser suites own host lifecycle.
import assert from 'node:assert/strict';
import {createServer} from 'node:http';
import {readFile} from 'node:fs/promises';
import {fileURLToPath} from 'node:url';
import {resolve,extname} from 'node:path';
const {chromium}=await import(process.env.PLAYWRIGHT_MODULE||'playwright');
const dist=fileURLToPath(new URL('../web/desktop/dist/',import.meta.url));
const posts=[],routes=[],benchPosts=[],errors=[];let releasePlan;
const storageKey='anvil-agents-desktop.local-chat.v1';
const wrappers=[{id:'launch',name:'Launch website',title:'Launch website',harness:'prime',workdir:'/fixture/launch',target:'native',wslDistro:'',connectAnvil:false,messages:[{role:'user',content:'Original launch brief'},{role:'assistant',content:'Saved launch answer'}],draft:''},{id:'camera',name:'Camera research',title:'Camera research',harness:'codex',workdir:'/fixture/camera',target:'native',wslDistro:'',connectAnvil:false,messages:[{role:'user',content:'Private camera transcript'},{role:'assistant',content:'Camera saved answer'}],draft:''}];
const snapshot={productTitle:'Anvil Agents Desktop',prefs:{harnessTarget:'native'},api:{reachable:false},harnessTarget:'native',wsl:{},localManager:{enabled:true,available:true},harnesses:[{id:'prime',displayName:'Prime Agent',present:true,delegatable:true,binaries:['prime-agent']},{id:'codex',displayName:'Codex',present:true,delegatable:true,binaries:['codex']}]};
const runtime={target:'native',availableHarnesses:['prime','codex'],now:'2026-10-25T18:00:00Z'};
const scenario={id:'switch',name:'Switch subjects',description:'An amendment after changing conversations.',runtime,defaultHarness:'prime',conversations:wrappers.map(c=>({id:c.id,label:c.name,summary:'Saved context',harness:c.harness,target:'native',status:'ready'})),turns:[{id:'turn',content:'Amend the camera plan',expect:{targets:['camera'],intents:['amend_running'],actions:['continue']}}]};
const json=(res,value,status=200)=>{res.writeHead(status,{'Content-Type':'application/json'});res.end(JSON.stringify(value));};
const emit=(res,event,data)=>res.write(`event: ${event}\ndata: ${JSON.stringify(data)}\n\n`);
async function body(req){let value='';for await(const chunk of req)value+=chunk;return JSON.parse(value);}
function decision(target='launch',manual=false){return {subjectChoice:manual?'':target,subjectConfidence:manual?0:.91,subjectProbabilities:manual?null:{launch:.91,camera:.06,new_subject:.02,none:.01},intent:manual?'unclassified':'amend_running',intentConfidence:manual?0:.95,intentProbabilities:manual?null:{amend_running:.95,status:.05},outcome:manual?'manual':'route',reason:manual?'user_selected':'confident',model:manual?undefined:'ui-fixture'};}
const server=createServer(async(req,res)=>{try{
 const path=new URL(req.url,'http://fixture').pathname;
 if(path==='/local/v1/manager/loop')return json(res,{revision:0,tasks:[],decisions:[],events:[]});
 if(path==='/local/v1/snapshot')return json(res,snapshot);
 if(path==='/local/v1/manager/route'){
  const request=await body(req);routes.push(request);assert.equal(req.headers.authorization,undefined);
  assert.ok(request.conversations.every(c=>!('workdir'in c)&&!('messages'in c)), 'Routing sends bounded descriptors, not native folder/transcript objects');
  res.writeHead(200,{'Content-Type':'text/event-stream'});emit(res,'inventory',{count:request.conversations.length});emit(res,'classifying',{available:true,manual:Boolean(request.targetId)});
  if(request.content==='Hold this selection')await new Promise(resolve=>{releasePlan=resolve;});
  const unavailable=request.content==='Unavailable classifier';
  const ambiguous=(request.content==='Pick that work back up'&&!request.targetId)||unavailable;
  const blocked=request.conversations.some(c=>c.status!=='ready');
  const status=request.content==='Check saved status';
  const selected=request.targetId||'launch';
  const d=decision(selected,Boolean(request.targetId));if(ambiguous){d.outcome='clarify';d.reason='low_confidence';d.subjectConfidence=.51;d.subjectProbabilities={launch:.51,camera:.48,new_subject:.01};}
  if(unavailable){d.intent='';d.intentConfidence=0;d.subjectProbabilities={};d.reason='provider_unavailable';}
  if(status){d.intent='status';d.intentConfidence=.99;}
  const action={kind:ambiguous?'clarify':status?'read_status':blocked?'wait':'continue',targetId:selected,reason:ambiguous?'low_confidence':status?'read_saved_state':blocked?'local_turn_unfinished':'continue_saved_conversation'};
  emit(res,'decision',d);emit(res,'action',action);
  emit(res,'result',{requestId:`route-${routes.length}`,decision:d,action,candidates:request.conversations.map(c=>({id:c.id,label:c.label,summary:c.summary})),latencyMs:17,snapshot:request,runtime});res.end();return;
 }
 if(path==='/local/v1/manager/loop/work'){const request=await body(req);posts.push(request);if(request.content==='Recover uncertain admission'&&posts.filter(p=>p.content===request.content).length===1)return json(res,{code:'storage_unavailable',message:'Admission outcome could not be confirmed.'},503);return json(res,{code:'fixture_unavailable',message:'Native execution is unavailable in this routing presentation fixture.'},400);}
 if(path==='/local/v1/manager/bench'){
  if(req.method==='GET')return json(res,{scenarios:[scenario],localManager:{enabled:true,available:true}});
  const request=await body(req);benchPosts.push(request);
  const replay=request.scenarios?.[0]||scenario;assert.ok(!request.scenarios||!replay.turns[0].targetId,'Regression must exercise inference instead of manual selection');
  const result={decision:decision('launch'),action:{kind:'continue',targetId:'launch',reason:'continue_saved_conversation'},candidates:replay.conversations,latencyMs:12};
  return json(res,{mode:request.mode,complete:request.mode==='fixture',errorReason:request.mode==='live'?'Fixture deadline reached':undefined,provenance:request.mode==='fixture'?'Scripted decision fixtures':'Live fixture transport',startedAt:runtime.now,scenarios:[{id:replay.id,name:replay.name,turns:[{id:'turn',content:replay.turns[0].content,expected:replay.turns[0].expect,actualTarget:'launch',actualIntent:'amend_running',targetPass:false,intentPass:true,actionPass:true,passed:false,judgmentSource:request.mode==='fixture'?'fixture':'provider',result,snapshot:{content:replay.turns[0].content,conversations:replay.conversations,context:replay.context||[],defaultHarness:replay.defaultHarness},runtime}]}],totals:{turns:1,passed:0,targetPassed:0,intentPassed:1,actionPassed:1,manualTurns:0,providerTurns:1}});
 }
 if(path.startsWith('/api/')||path==='/ui-config.json')throw Error('Local Manager unexpectedly used remote auth/API');
 const asset=path.startsWith('/assets/')?resolve(dist,`.${path}`):resolve(dist,'index.html');res.setHeader('Content-Type',({'.js':'application/javascript','.css':'text/css','.html':'text/html'})[extname(asset)]||'application/octet-stream');res.end(await readFile(asset));
 }catch(error){errors.push(error.message);if(!res.headersSent)res.writeHead(500);res.end();}});
await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));const origin=`http://127.0.0.1:${server.address().port}`;
const browser=await chromium.launch({headless:true,...(process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE?{executablePath:process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE}:{})});
try{
 const context=await browser.newContext({viewport:{width:1500,height:1000}});await context.addInitScript(({storageKey,wrappers})=>{if(!localStorage.getItem(storageKey))localStorage.setItem(storageKey,JSON.stringify(wrappers));},{storageKey,wrappers});
 const page=await context.newPage();page.setDefaultTimeout(10000);page.on('pageerror',e=>errors.push(e.message));
 const send=async text=>{await page.getByRole('textbox',{name:'Message the manager',exact:true}).fill(text);await page.getByRole('button',{name:'Route message',exact:true}).click();};
 const stored=()=>page.evaluate(key=>JSON.parse(localStorage.getItem(key)),storageKey);
 await page.goto(`${origin}/local-manager`);await page.getByRole('heading',{name:'Local Manager',exact:true}).waitFor();
 await send('Hold this selection');await page.getByRole('complementary',{name:'Routing activity'}).getByText('Classifier requested',{exact:true}).waitFor();assert.equal(posts.length,0);
 const direct=await context.newPage();direct.on('pageerror',e=>errors.push(e.message));await direct.goto(`${origin}/chat`);await direct.getByRole('button',{name:'Open Launch website',exact:true}).click();await direct.locator('details.agent-settings summary').click();await direct.getByRole('textbox',{name:'Working folder',exact:true}).fill('/fixture/changed');await direct.waitForFunction(key=>JSON.parse(localStorage.getItem(key)).find(c=>c.id==='launch').workdir==='/fixture/changed',storageKey);
 releasePlan();await page.getByText(/conversation changed while routing/).first().waitFor();assert.equal(posts.length,0,'Stale routing cannot deliver to a changed folder');assert.equal((await stored()).find(c=>c.id==='launch').messages.length,2);
 await send('Pick that work back up');await page.getByRole('button',{name:'Send to Launch website',exact:true}).waitFor();assert.equal(posts.length,0);
 await page.getByRole('button',{name:'Send to Launch website',exact:true}).click();await page.getByText('Human choice · no model probability',{exact:true}).waitFor();await page.getByText('Native execution is unavailable in this routing presentation fixture.',{exact:true}).first().waitFor();assert.equal(posts.length,1);assert.ok(posts[0].conversation.messages.some(m=>m.content==='Saved launch answer'));assert.ok(!posts[0].conversation.messages.some(m=>m.content==='Private camera transcript'));
 await page.waitForFunction(key=>JSON.parse(localStorage.getItem(key)).find(c=>c.id==='launch').pending!==true,storageKey);assert.equal((await stored()).find(c=>c.id==='launch').messages.length,2,'Rejected host admission preserves history');
 await page.getByRole('button',{name:'Report wrong route',exact:true}).click();await page.getByRole('combobox',{name:'Expected conversation',exact:true}).selectOption('camera');await page.getByRole('combobox',{name:'Expected intent',exact:true}).selectOption('amend_running');
 const downloadPromise=page.waitForEvent('download');await page.getByRole('button',{name:'Export regression JSON',exact:true}).click();const download=await downloadPromise;const exported=JSON.parse(await readFile(await download.path(),'utf8'));assert.ok(Array.isArray(exported));assert.deepEqual(exported[0].turns[0].expect.targets,['camera']);assert.equal(exported[0].turns[0].targetId,undefined);assert.ok(exported[0].conversations.every(c=>!('workdir'in c)&&!('messages'in c)));
 await page.getByRole('link',{name:'Routing scenarios',exact:true}).click();await page.getByRole('button',{name:'Run all scenarios',exact:true}).click();await page.getByRole('heading',{name:'Fixture replay · 0/1 passed',exact:true}).waitFor();await page.getByText('Saved regression drafts',{exact:true}).waitFor();assert.equal(posts.length,1,'Bench never executes native work');
 await page.getByRole('button',{name:'Rerun correction live',exact:true}).last().click();await page.getByRole('heading',{name:'Partial · Live classifier replay · 0/1 passed',exact:true}).waitFor();await page.getByRole('alert').getByText(/This replay did not finish/).waitFor();assert.equal(benchPosts.at(-1).mode,'live');assert.deepEqual(benchPosts.at(-1).scenarios[0].turns[0].expect.targets,['camera']);assert.equal(posts.length,1);
 await page.getByRole('link',{name:'Back to Local Manager',exact:true}).click();await send('Unavailable classifier');await page.getByRole('button',{name:'Send to Launch website',exact:true}).last().waitFor();await page.getByRole('button',{name:'Report wrong route',exact:true}).click();assert.equal(await page.getByRole('combobox',{name:'Expected intent',exact:true}).inputValue(),'question');await page.getByRole('button',{name:'Save regression draft',exact:true}).click();const latestDraft=await page.evaluate(()=>JSON.parse(localStorage.getItem('anvil-agents-desktop.local-routing-regressions.v1')).at(-1));assert.deepEqual(latestDraft.scenario.turns[0].expect.intents,['question']);assert.equal(posts.length,1);
 await send('Recover uncertain admission');await page.getByText('Admission outcome could not be confirmed.',{exact:true}).first().waitFor();
 const uncertain=(await stored()).find(c=>c.id==='launch');assert.equal(uncertain.pending,true,'A server storage error is not proof of rejected admission');assert.ok(uncertain.managerRequestId);assert.ok(uncertain.managerSubmission);
 await page.getByRole('button',{name:'Recover saved submission',exact:true}).click();await page.waitForFunction(key=>!JSON.parse(localStorage.getItem(key)).find(c=>c.id==='launch').managerRequestId,storageKey);assert.deepEqual(posts.at(-2),posts.at(-1),'Recovery must reuse the exact frozen body and request ID');
 assert.deepEqual(errors,[]);console.log('PASS observed routing, stale folder rejection, manual routing, rejected admission preserves history, regression export and live replay UI');
}finally{await browser.close();server.closeAllConnections();await new Promise(resolve=>server.close(resolve));}

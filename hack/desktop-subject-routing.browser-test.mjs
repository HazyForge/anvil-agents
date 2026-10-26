#!/usr/bin/env node
// Browser contract: real chunked HTTP proves routing is visible before a
// decision arrives, uncertain choices retain the request ID, and an interrupted
// stream cannot become a successful handoff. Existing direct-chat tests do not
// cross this manager protocol. Fixtures provide network inputs only; the built
// application owns stream parsing, rendering, selection and recovery.
// No model, OIDC service, Kubernetes or live runner is contacted.
import assert from 'node:assert/strict';
import { createServer } from 'node:http';
import { readFile, mkdir } from 'node:fs/promises';
import { fileURLToPath } from 'node:url';
import { resolve, extname, dirname } from 'node:path';
const { chromium } = await import(process.env.PLAYWRIGHT_MODULE || 'playwright');
const dist = fileURLToPath(new URL('../web/desktop/dist/', import.meta.url));
const stamp = () => new Date().toISOString();
const profileName = 'desktop-standing-assistant';
const subjects = [
  {id:'00000000-0000-4000-8000-000000000001',kind:'project',label:'Website launch',summary:'Review launch copy and finish the product page.',threadId:'launch-thread',profileName,status:'open',touchedAt:stamp()},
  {id:'00000000-0000-4000-8000-000000000002',kind:'idea',label:'Research notebook',summary:'Compare ideas for the next research session.',threadId:'research-thread',profileName,status:'open',touchedAt:stamp()},
];
const receipts = new Map(), threads = new Map(), posts = [], errors = [];
const fixtureToken = 'manager-browser-fixture-not-an-access-token';
let enabled = true, rosterDenied = false, decisionRelease;
const decisionGate = new Promise(resolve => { decisionRelease = resolve; });
const config = {defaultNamespaces:['anvilhub','other-workspace'],oidc:{issuer:'https://fixture.invalid',clientId:'fixture-console',audiences:['fixture'],scopes:['openid']},desktop:{oidcClientId:'fixture-native'},composition:{readEnabled:true,writeEnabled:false},runs:{createEnabled:true},chat:{enabled:true,subjectRoutingEnabled:true}};
function json(res, body, status=200) {res.writeHead(status, {'Content-Type':'application/json'});res.end(JSON.stringify(body));}
function emit(res, event, data) {res.write(`event: ${event}\ndata: ${JSON.stringify(data)}\n\n`);}
async function readJSON(req) {let text='';for await(const chunk of req) text+=chunk;return JSON.parse(text);}
function threadFor(subject) {
  if(!threads.has(subject.threadId)) threads.set(subject.threadId,{id:subject.threadId,namespace:'anvilhub',profileName,mode:'persona',title:subject.label,createdBy:'fixture',createdAt:stamp(),updatedAt:stamp(),messages:[],turns:[]});
  return threads.get(subject.threadId);
}
const server = createServer(async (req,res) => {
  try {
    const url=new URL(req.url,'http://fixture'),path=url.pathname;
    if(path==='/local/v1/snapshot') return json(res,{productTitle:'Anvil Agents Desktop',prefs:{apiOrigin:origin},api:{origin,reachable:true},harnesses:[],wsl:{},harnessTarget:'native'});
    if(path==='/ui-config.json') return json(res,{...config,chat:{enabled:true,subjectRoutingEnabled:enabled}});
    if(!path.startsWith('/api/')) {
      const asset=path.startsWith('/assets/')?resolve(dist,`.${path}`):resolve(dist,'index.html');
      res.setHeader('Content-Type',({'.js':'application/javascript','.css':'text/css','.html':'text/html'})[extname(asset)]||'application/octet-stream');
      return res.end(await readFile(asset));
    }
    assert.equal(req.headers.authorization,`Bearer ${fixtureToken}`);
    assert.equal(url.searchParams.has('access_token'),false);
    const [,ns,tail]=path.match(/^\/api\/v1\/namespaces\/([^/]+)\/(.*)$/)||[];
    if(tail==='agent-run-profiles') return rosterDenied ? json(res,{error:{code:'not_found',message:'Agent roster unavailable'}},404) : json(res,{items:[{kind:'AgentRunProfile',metadata:{name:profileName},spec:{harnessProfileRef:{name:'codex'}}}]});
    if(tail==='chat/subjects' && req.method==='GET') return json(res,{enabled,items:ns==='anvilhub'?subjects:[]});
    if(tail==='chat/routing') return json(res,{items:ns==='anvilhub'?[...receipts.values()]:[]});
    if(tail?.startsWith('chat/subjects/') && req.method==='POST') {
      const subject=subjects.find(item=>item.id===tail.split('/')[2]);assert.ok(subject);
      if(tail.endsWith('/merge')) {const body=await readJSON(req);subject.status='merged';return json(res,subjects.find(item=>item.id===body.targetId));}
      Object.assign(subject,await readJSON(req));return json(res,subject);
    }
    if(tail==='chat/manager/messages') {
      const body=await readJSON(req);posts.push(body);
      if(Buffer.byteLength(body.content)>65536) return json(res,{error:{code:'invalid_body',message:'This message is larger than 64 KiB. Shorten it before sending.'}},400);
      if(body.content==='Rejected before admission.') return json(res,{error:{code:'invalid_target',message:'Choose an available agent before sending.'}},400);
      const record=receipts.get(body.requestId)||{id:body.requestId,content:body.content,profileName:body.profileName,status:'routing',events:[],createdAt:stamp()};
      receipts.set(body.requestId,record);record.status='routing';record.events=[];
      res.writeHead(200,{'Content-Type':'text/event-stream','Cache-Control':'no-cache','Connection':'keep-alive'});
      const step=(name,label,status='completed',extra={})=>{const event={step:name,label,status,at:stamp(),...extra};record.events.push(event);emit(res,'routing',event);};
      step('received','Message received');
      step('subjects','Recent subjects loaded','completed',{candidates:subjects.map(({id,label,summary})=>({id,label,summary}))});
      step('classifying','Matching your message','running');
      if(body.content==='Update the website launch copy.' && posts.length===1) await decisionGate;
      if(body.content==='Recover this routing connection.' && posts.filter(post=>post.requestId===body.requestId).length===1) return res.end();
      if(body.content==='Pick that work back up.' && !body.subjectId) {
        step('decision','Two subjects could fit','waiting',{decision:{subjectChoice:subjects[0].id,subjectConfidence:.51,subjectProbabilities:{[subjects[0].id]:.51,[subjects[1].id]:.47,new_subject:.02},intent:'amend_running',intentConfidence:.9,outcome:'clarify'}});
        record.status='clarify';record.result={requestId:body.requestId,status:'clarify',events:record.events,clarification:'Which work should continue?'};
        emit(res,'result',record.result);return res.end();
      }
      const subject=subjects.find(item=>item.id===body.subjectId)||subjects[0],thread=threadFor(subject);
      step('decision','Subject and intent selected','completed',{decision:{subjectChoice:subject.id,subjectConfidence:body.subjectId?0:.94,...(body.subjectId?{reason:'user_selected'}:{subjectProbabilities:{[subject.id]:.94,[subjects.find(item=>item.id!==subject.id).id]:.04,new_subject:.02}}),intent:'amend_running',intentConfidence:.96,outcome:'route',model:'fixture-decision-provider'}});
      const user={id:`user-${body.requestId}`,threadId:thread.id,role:'user',content:body.content,sequence:thread.messages.length+1,createdAt:stamp()};
      const turn={id:`turn-${body.requestId}`,runName:`run-${body.requestId}`,requestId:body.requestId,userMessageId:user.id,status:body.content==='Background launch work.'?'running':'succeeded',createdAt:stamp()};
      if(!thread.turns.some(item=>item.id===turn.id)) {
        thread.turns.push(turn);thread.messages.push(user);
        if(turn.status==='succeeded')thread.messages.push({id:`answer-${body.requestId}`,threadId:thread.id,role:'assistant',content:body.subjectId===subjects[1].id?'I found the research notebook and its last open question.':'The launch thread is ready. I found the saved copy review.',sequence:thread.messages.length+1,createdAt:stamp(),metadata:{turnId:turn.id,runName:turn.runName}});
      }
      step('dispatch','Handed to the subject conversation');
      Object.assign(record,{status:'accepted',subjectId:subject.id,threadId:thread.id,turnId:turn.id});
      record.result={requestId:body.requestId,status:'accepted',subject,thread:{...thread,messages:undefined,turns:undefined},user,turn,events:record.events};
      if(body.content==='Recover accepted receipt.' && posts.filter(post=>post.requestId===body.requestId).length===1) {
        const result={...record.result,status:'failed',error:'The routing receipt could not be saved. Retry this same message to recover its execution.'};record.status='routing';delete record.result;emit(res,'result',result);return res.end();
      }
      emit(res,'result',record.result);return res.end();
    }
    const threadMatch=tail?.match(/^chat\/threads\/([^/]+)(\/stream)?$/);
    if(threadMatch) {
      const thread=threads.get(threadMatch[1]);if(!thread)return json(res,{},404);
      if(!threadMatch[2])return json(res,thread);
      res.writeHead(200,{'Content-Type':'text/event-stream'});emit(res,'snapshot',{type:'snapshot',thread,messages:thread.messages,turns:thread.turns});emit(res,'terminal',{type:'terminal',code:'job_plane'});return res.end();
    }
    if(tail?.endsWith('/events')) {res.writeHead(200,{'Content-Type':'text/event-stream'});emit(res,'terminal',{type:'terminal',run:{phase:'Succeeded'}});return res.end();}
    return json(res,{error:{code:'not_found',message:'Fixture endpoint unavailable'}},404);
  } catch(error) {errors.push(error.message);if(!res.headersSent)res.writeHead(500);res.end();}
});
await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));
const origin=`http://127.0.0.1:${server.address().port}`;
const browser=await chromium.launch({headless:true,...(process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE?{executablePath:process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE}:{})});
try {
  const context=await browser.newContext({viewport:{width:1540,height:1040}});
  await context.addInitScript(token=>{localStorage.setItem('anvil-agents-desktop.chat-location','remote');sessionStorage.setItem('anvil-agents-desktop.accessToken',token);sessionStorage.setItem('anvil-agents-desktop.expiresAt',String(Date.now()+3600000));},fixtureToken);
  const page=await context.newPage();page.setDefaultTimeout(10000);page.on('pageerror',error=>errors.push(error.message));
  await page.goto(origin);
  await page.getByRole('heading',{name:'Your work, in context.'}).waitFor();assert.ok(page.url().endsWith('/manager'));
  await page.getByLabel('Message the manager').fill('界'.repeat(21846));await page.getByRole('button',{name:'Send ↗',exact:true}).click();
  await page.getByRole('alert').filter({hasText:'Shorten it before sending.'}).waitFor();assert.equal(posts.length,0,'oversize UTF-8 must be rejected before submitting');
  await page.getByLabel('Message the manager').fill('Update the website launch copy.');await page.getByRole('button',{name:'Send ↗',exact:true}).click();
  await page.getByText('Matching your message',{exact:true}).waitFor();
  assert.equal(await page.getByText('Subject and intent selected',{exact:true}).count(),0,'decision must not appear before its event arrives');
  assert.equal(await page.getByText('Handed off',{exact:true}).count(),0);
  decisionRelease();
  await page.getByText('The launch thread is ready. I found the saved copy review.',{exact:true}).waitFor();
  assert.equal(await page.getByLabel('Subject probabilities').locator('meter').count(),3);
  await page.getByLabel('Message the manager').fill('Pick that work back up.');await page.getByRole('button',{name:'Send ↗',exact:true}).click();
  await page.getByText('Which work should continue?',{exact:true}).waitFor();
  await page.locator('.manager-clarification-options').getByRole('button',{name:'Research notebook',exact:true}).click();
  await page.getByText('I found the research notebook and its last open question.',{exact:true}).waitFor();
  await page.getByText('Subject selected by you',{exact:true}).waitFor();assert.equal(await page.getByLabel('Subject probabilities').count(),0,'a user selection must not pretend to be a model probability');
  assert.equal(posts[1].requestId,posts[2].requestId,'clarification must refine the original request');assert.equal(posts[2].subjectId,subjects[1].id);assert.equal(receipts.size,2);
  await page.getByLabel('Message the manager').fill('Recover this routing connection.');await page.getByRole('button',{name:'Send ↗',exact:true}).click();
  await page.getByRole('button',{name:'Recover this message',exact:true}).waitFor();
  assert.equal(await page.getByRole('button',{name:'Send ↗',exact:true}).isDisabled(),true,'unknown acceptance cannot produce a new request');
  await page.getByRole('button',{name:'Recover this message',exact:true}).click();
  await page.getByText('Handed off',{exact:true}).last().waitFor();
  assert.equal(posts[3].requestId,posts[4].requestId);assert.equal(receipts.size,3);
  await page.getByLabel('Message the manager').fill('Recover accepted receipt.');await page.getByRole('button',{name:'Send ↗',exact:true}).click();
  await page.getByRole('button',{name:'Recover this message',exact:true}).waitFor();assert.equal(await page.getByRole('button',{name:'Send ↗',exact:true}).isDisabled(),true,'an accepted turn with failed receipt persistence still requires recovery');
  await page.getByRole('button',{name:'Recover this message',exact:true}).click();await page.getByText('Handed off',{exact:true}).last().waitFor();
  assert.equal(posts[5].requestId,posts[6].requestId);assert.equal(threads.get(subjects[0].threadId).turns.filter(turn=>turn.requestId===posts[5].requestId).length,1);
  await page.reload();await page.getByText('I found the research notebook and its last open question.',{exact:true}).waitFor();
  assert.equal(await page.locator('.manager-message').count(),4,'history survives browser reload');
  await page.getByLabel('Message the manager').fill('Rejected before admission.');await page.getByRole('button',{name:'Send ↗',exact:true}).click();
  await page.getByRole('alert').filter({hasText:'Choose an available agent'}).waitFor();assert.equal(await page.getByRole('button',{name:'Send ↗',exact:true}).isDisabled(),false,'definite rejection permits editing and sending');
  await page.getByLabel('Message the manager').fill('Background launch work.');await page.getByRole('button',{name:'Send ↗',exact:true}).click();
  await page.getByText('Message handed off. Waiting for the agent’s reply.',{exact:true}).waitFor();
  await page.getByRole('button',{name:/Research notebook.*idea/i}).click();
  await page.getByLabel('Message the manager').fill('Continue the research notebook.');await page.getByRole('button',{name:'Send ↗',exact:true}).click();
  await page.getByText('I found the research notebook and its last open question.',{exact:true}).last().waitFor();
  await page.getByRole('button',{name:/All subjects.*Let the manager choose/i}).click();
  const backgroundThread=threads.get(subjects[0].threadId),backgroundTurn=backgroundThread.turns.find(turn=>turn.status==='running');assert.ok(backgroundTurn);
  backgroundTurn.status='succeeded';backgroundThread.messages.push({id:'background-answer',threadId:backgroundThread.id,role:'assistant',content:'Background launch work finished while you were on research.',sequence:backgroundThread.messages.length+1,createdAt:stamp(),metadata:{turnId:backgroundTurn.id,runName:backgroundTurn.runName}});
  await page.getByText('Background launch work finished while you were on research.',{exact:true}).waitFor();
  const composer=await page.locator('.manager-composer').boundingBox();assert.ok(composer && composer.y+composer.height<=1040,'composer stays visible while conversation scrolls');
  const screenshot=process.env.MANAGER_SCREENSHOT || '/tmp/anvil-manager-routing-desktop.png';await mkdir(dirname(screenshot),{recursive:true});await page.screenshot({path:screenshot,fullPage:true});
  await page.getByRole('button',{name:/Website launch.*project/i}).click();await page.getByRole('button',{name:'Park',exact:true}).click();
  await page.getByRole('button',{name:'Reopen',exact:true}).waitFor();assert.equal(subjects[0].status,'parked');
  await page.getByRole('button',{name:'Edit',exact:true}).click();await page.getByLabel('Subject name').fill('Launch copy review');await page.getByLabel('Subject kind').selectOption('preference');await page.getByLabel('Subject aliases').fill('Launch, Copy style');await page.getByRole('button',{name:'Save',exact:true}).click();
  await page.getByRole('heading',{name:'Launch copy review',exact:true}).waitFor();
  assert.equal(subjects[0].kind,'preference');assert.deepEqual(subjects[0].aliases,['Launch','Copy style']);
  await page.getByText('Merge with another subject',{exact:true}).click();await page.getByLabel('Merge destination').selectOption(subjects[1].id);await page.getByRole('button',{name:'Merge into selected subject',exact:true}).click();
  await page.getByRole('heading',{name:'Research notebook',exact:true}).waitFor();assert.equal(await page.getByRole('button',{name:/Launch copy review.*preference/i}).count(),0);
  rosterDenied=true;await page.reload();await page.getByText('Could not load agents for new subjects.',{exact:false}).waitFor();
  assert.equal(await page.getByRole('heading',{name:'Your work, in context.'}).count(),1,'roster denial must not hide the manager vault');
  await page.getByRole('button',{name:/Research notebook.*idea/i}).click();await page.getByLabel('Message the manager').fill('An existing subject still has its agent.');assert.equal(await page.getByRole('button',{name:'Send ↗',exact:true}).isDisabled(),false);rosterDenied=false;
  await page.getByLabel('Manager workspace').selectOption('other-workspace');await page.getByText('Subjects will grow from your conversations.',{exact:false}).waitFor();
  assert.equal(await page.locator('.manager-message').count(),0,'workspace switch clears prior transcript');
  await page.setViewportSize({width:720,height:1000});assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth>window.innerWidth),false,'narrow layout must not overflow horizontally');
  enabled=false;await page.reload();await page.getByRole('heading',{name:'Subject routing is not enabled here yet.'}).waitFor();
  assert.equal(posts.length,10);assert.deepEqual(errors,[]);
  console.log(JSON.stringify({passed:true,requests:receipts.size,posts:posts.length,screenshot,checks:['UTF-8 size prevalidation','incremental SSE before decision','explicit clarification reuses request','interrupted stream requires recovery','accepted turn receipt recovery','HTTP rejection remains editable','history reload','background subject completion','composer stays visible','subject update and merge','roster denial preserves vault','workspace isolation','narrow viewport','disabled gate']}));
} finally {decisionRelease();await browser.close();await new Promise(resolve=>server.close(resolve));}

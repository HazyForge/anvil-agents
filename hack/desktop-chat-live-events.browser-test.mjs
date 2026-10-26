#!/usr/bin/env node
// UI contract test for live chat updates: an isolated browser against an
// in-memory HTTP + WebSocket fixture that plays the anvil-agents API. It
// proves that outside changes (a peer's reply, a finished turn) reach the
// open conversation through the chat events stream without waiting for a
// poll, that reading history is never interrupted, and that live tokens hand
// off to the durable reply exactly once. No real OIDC, runner, or model.
// Build web/desktop first; set PLAYWRIGHT_MODULE to Playwright's index.mjs
// when it is not resolvable from here.
import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { createServer } from 'node:http';
import { readFile } from 'node:fs/promises';
import { fileURLToPath } from 'node:url';
import { resolve, extname } from 'node:path';
const { chromium } = await import(process.env.PLAYWRIGHT_MODULE || 'playwright');

const dist = fileURLToPath(new URL('../web/desktop/dist/', import.meta.url));
const TOKEN = 'live-events-fixture-not-a-token';
const NS = 'anvilhub';
const THREAD = 'standing-alpha';
const errors = [];
const json = (res, status, body) => { res.writeHead(status, {'Content-Type': 'application/json'}); res.end(JSON.stringify(body)); };

const now = () => new Date().toISOString();
const thread = {id: THREAD, namespace: NS, profileName: 'agent-alpha', mode: 'persona', title: 'agent-alpha', createdBy: 'fixture', createdAt: now(), updatedAt: now(), messages: [], turns: []};
for (let i = 1; i <= 24; i++) {
  thread.messages.push({id: `m${i}`, threadId: THREAD, role: i % 2 ? 'user' : 'assistant', content: `History line ${i}. ${'Context for the fixture conversation. '.repeat(3)}`, sequence: i, createdAt: now()});
}
const appendMessage = (role, content, metadata) => {
  thread.messages.push({id: `m${thread.messages.length + 1}`, threadId: THREAD, role, content, sequence: thread.messages.length + 1, createdAt: now(), metadata});
  thread.updatedAt = now();
};

const eventStreams = new Set();
let eventConnections = 0;
let detailGets = 0;
const sockets = new Set();
const pushEvent = (event, data) => { for (const res of eventStreams) res.write(`event: ${event}\ndata: ${JSON.stringify(data)}\n\n`); };
const wsFrame = text => {
  const payload = Buffer.from(text);
  const header = payload.length < 126 ? Buffer.from([0x81, payload.length]) : Buffer.from([0x81, 126, payload.length >> 8, payload.length & 255]);
  return Buffer.concat([header, payload]);
};
const sendWS = value => { for (const socket of sockets) socket.write(wsFrame(JSON.stringify(value))); };

const server = createServer(async (req, res) => {
  const url = new URL(req.url, 'http://fixture');
  const path = url.pathname;
  if (url.searchParams.has('access_token')) errors.push(`token in query: ${req.url}`);
  if (path === '/local/v1/snapshot') return json(res, 200, {productTitle: 'Anvil Agents Desktop', prefs: {apiOrigin: 'http://fixture'}, api: {origin: 'http://fixture', reachable: true}, harnesses: [], wsl: {}, harnessTarget: 'native'});
  if (path === '/ui-config.json') return json(res, 200, {defaultNamespaces: [NS], oidc: {issuer: 'https://fixture.invalid', clientId: 'fixture', audiences: ['fixture'], scopes: ['openid']}, desktop: {oidcClientId: 'fixture-native'}, composition: {readEnabled: true, writeEnabled: false}, runs: {createEnabled: false}, chat: {enabled: true}});
  if (path.startsWith('/api/')) {
    if (req.headers.authorization !== `Bearer ${TOKEN}`) { errors.push(`missing bearer on ${path}`); return json(res, 401, {error: {code: 'unauthorized'}}); }
    const tail = path.slice(`/api/v1/namespaces/${NS}/`.length);
    if (tail === 'agent-run-profiles') return json(res, 200, {items: [{metadata: {name: 'agent-alpha'}, spec: {harnessProfileRef: {name: 'codex-standard'}}}]});
    if (tail === 'agent-harness-profiles') return json(res, 200, {items: [{metadata: {name: 'codex-standard'}, spec: {backend: {kind: 'codex'}}}]});
    if (tail === 'chat/threads' && req.method === 'GET') return json(res, 200, {items: [{...thread, messages: undefined, turns: undefined}]});
    if (tail === 'chat/threads' && req.method === 'POST') return json(res, 200, {...thread, messages: undefined, turns: undefined});
    if (tail === `chat/threads/${THREAD}` && req.method === 'GET') { detailGets++; return json(res, 200, structuredClone(thread)); }
    if (tail === 'chat/events') {
      eventConnections++;
      res.writeHead(200, {'Content-Type': 'text/event-stream', 'Cache-Control': 'no-store'});
      res.write(`event: ready\ndata: ${JSON.stringify({type: 'ready', namespace: NS})}\n\n`);
      eventStreams.add(res);
      req.on('close', () => eventStreams.delete(res));
      return;
    }
    if (/^agent-runs\/[^/]+\/events$/.test(tail)) return json(res, 404, {error: {code: 'not_found', message: 'Runner has not appeared yet'}});
    return json(res, 404, {error: {code: 'not_found'}});
  }
  const asset = path.startsWith('/assets/') ? resolve(dist, `.${path}`) : resolve(dist, 'index.html');
  try {
    res.setHeader('Content-Type', ({'.js': 'application/javascript', '.css': 'text/css', '.html': 'text/html', '.woff2': 'font/woff2', '.woff': 'font/woff'})[extname(asset)] || 'application/octet-stream');
    res.end(await readFile(asset));
  } catch { res.writeHead(404).end(); }
});
server.on('upgrade', (req, socket) => {
  const protocols = String(req.headers['sec-websocket-protocol'] || '').split(',').map(item => item.trim());
  if (req.url.includes('access_token')) errors.push(`token in WebSocket URL: ${req.url}`);
  if (!req.url.startsWith(`/api/v1/namespaces/${NS}/chat/threads/${THREAD}/stream`) || !protocols.includes('anvil-agents.chat-stream.v1') || !protocols.includes(`bearer.${TOKEN}`)) {
    socket.end('HTTP/1.1 401 Unauthorized\r\n\r\n');
    return;
  }
  const accept = createHash('sha1').update(`${req.headers['sec-websocket-key']}258EAFA5-E914-47DA-95CA-C5AB0DC85B11`).digest('base64');
  socket.write(`HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: ${accept}\r\nSec-WebSocket-Protocol: anvil-agents.chat-stream.v1\r\n\r\n`);
  sockets.add(socket);
  socket.on('close', () => sockets.delete(socket));
  socket.on('error', () => sockets.delete(socket));
  socket.write(wsFrame(JSON.stringify({type: 'snapshot', thread: {id: THREAD}, messages: [], messageTotal: thread.messages.length, turns: thread.turns, standing: {sessionName: 'fixture', harness: 'codex', warm: true, resumes: 0}})));
});
await new Promise(done => server.listen(0, '127.0.0.1', done));
const origin = `http://127.0.0.1:${server.address().port}`;

const browser = await chromium.launch({headless: true, ...(process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE ? {executablePath: process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE} : {})});
const context = await browser.newContext({viewport: {width: 1280, height: 820}});
await context.addInitScript(token => {
  localStorage.setItem('anvil-agents-desktop.chat-location', 'remote');
  sessionStorage.setItem('anvil-agents-desktop.accessToken', token);
  sessionStorage.setItem('anvil-agents-desktop.expiresAt', String(Date.now() + 3600000));
}, TOKEN);
const page = await context.newPage();
page.setDefaultTimeout(10000);
page.on('pageerror', error => errors.push(error.message));
const until = async (check, message, timeout = 4000) => {
  const start = Date.now();
  while (Date.now() - start < timeout) { if (await check()) return; await new Promise(done => setTimeout(done, 50)); }
  assert.fail(message);
};
const transcript = () => page.evaluate(() => { const el = document.querySelector('.chat-messages'); return {top: el.scrollTop, height: el.scrollHeight, view: el.clientHeight}; });
// LIVE_EVENTS_SHOTS=<dir> saves screenshots of each state for review.
const shot = async name => { if (process.env.LIVE_EVENTS_SHOTS) await page.screenshot({path: `${process.env.LIVE_EVENTS_SHOTS}/${name}.png`}); };
const atBottom = async () => { const t = await transcript(); return t.height - (t.top + t.view) < 8; };

try {
  await page.goto(`${origin}/chat`);
  await page.getByText('History line 24.', {exact: false}).waitFor();
  await page.locator('.chat-sync-live').waitFor();
  await until(atBottom, 'the conversation did not open at its newest message');

  // An outside reply lands through the events stream, not the 20s idle read.
  const getsBefore = detailGets;
  appendMessage('assistant', 'Peer review finished: 14 checks passed.');
  const pushedAt = Date.now();
  pushEvent('thread', {type: 'thread', threadId: THREAD, reason: 'message'});
  await page.getByText('Peer review finished: 14 checks passed.').waitFor({timeout: 1500});
  assert.ok(Date.now() - pushedAt < 1500, 'outside reply waited for a poll');
  assert.ok(detailGets > getsBefore, 'the change notice did not trigger a thread read');
  assert.equal(await page.locator('.chat-bubble-enter').filter({hasText: 'Peer review finished'}).count(), 1);
  await until(atBottom, 'the view did not follow a reply that arrived at the bottom');
  await shot('live-reply-arrived');

  // Reading history is never interrupted; a pill offers the way back.
  await page.evaluate(() => { document.querySelector('.chat-messages').scrollTop = 0; });
  await page.waitForTimeout(150);
  appendMessage('user', 'agent-beta asked for the deploy log.', {authorProfile: 'agent-beta'});
  pushEvent('thread', {type: 'thread', threadId: THREAD, reason: 'message'});
  await page.getByRole('button', {name: '1 new message'}).waitFor({timeout: 1500});
  assert.ok((await transcript()).top < 8, 'reading position moved while scrolled up');
  await shot('live-new-messages-pill');
  await page.getByRole('button', {name: '1 new message'}).click();
  await until(atBottom, 'jumping to new messages did not reach the bottom');
  await page.getByRole('button', {name: '1 new message'}).waitFor({state: 'detached'});

  // A standing turn streams live tokens, then hands off to the durable reply.
  thread.turns.push({id: 'turn-live', requestId: 'turn-live', status: 'running', runName: 'run-live', createdAt: now()});
  thread.activeTurn = thread.turns.at(-1);
  thread.updatedAt = now();
  pushEvent('thread', {type: 'thread', threadId: THREAD, reason: 'turn', turnId: 'turn-live'});
  await until(() => sockets.size > 0, 'no live token stream opened for the running turn');
  ['Streaming ', 'reply ', 'arrives'].forEach((token, seq) => sendWS({type: 'token', threadId: THREAD, turnId: 'turn-live', runName: 'run-live', seq, token, done: false}));
  const live = page.locator('.chat-bubble-live');
  await live.filter({hasText: 'Streaming reply arrives'}).waitFor({timeout: 2000});
  assert.equal(await live.getAttribute('aria-busy'), 'true');
  await shot('live-streaming-reply');
  appendMessage('assistant', 'Streaming reply arrives in full.', {runName: 'run-live'});
  thread.turns.at(-1).status = 'succeeded';
  delete thread.activeTurn;
  sendWS({type: 'token', threadId: THREAD, turnId: 'turn-live', runName: 'run-live', seq: 3, token: '', done: true});
  sendWS({type: 'terminal', code: 'standing_ready'});
  await page.getByText('Streaming reply arrives in full.').waitFor({timeout: 2000});
  await live.waitFor({state: 'detached', timeout: 2000});
  assert.equal(await page.getByText(/Streaming reply arrives/).count(), 1, 'live text and durable reply were both shown');
  assert.equal(await page.locator('.chat-bubble-enter').filter({hasText: 'Streaming reply arrives in full.'}).count(), 0, 'the hand-off from live text animated as a new message');

  // A dropped events connection reconnects and catches up on what it missed.
  const connections = eventConnections;
  const readsBeforeDrop = detailGets;
  appendMessage('assistant', 'Written while the events stream was down.');
  for (const res of eventStreams) res.end();
  await until(() => eventConnections > connections, 'the events stream did not reconnect');
  await page.getByText('Written while the events stream was down.').waitFor({timeout: 3000});
  assert.ok(detailGets > readsBeforeDrop);
  await page.locator('.chat-sync-live').waitFor();

  assert.deepEqual(errors, []);
  console.log('PASS live chat: outside replies arrive without polling, reading position is kept with a jump pill, live tokens hand off once, events reconnect and catch up, no tokens in URLs');
} finally {
  for (const res of eventStreams) res.end();
  for (const socket of sockets) socket.destroy();
  await browser.close();
  server.close();
}

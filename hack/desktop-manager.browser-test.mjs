#!/usr/bin/env node
// UI contract test for the Desktop Manager view (one chat, protocol v1 in
// docs/one-chat-remote.md). An isolated browser talks to an in-memory fixture
// that plays both the Desktop host bridge (/local/v1/onechat/*) and a scripted
// `onechat serve`. No real manager, harness, model or ZITADEL is used.
// Build web/desktop first; set PLAYWRIGHT_MODULE to Playwright's index.mjs
// when it is not resolvable from here.
import assert from 'node:assert/strict';
import { createHash, randomUUID } from 'node:crypto';
import { createServer } from 'node:http';
import { readFile } from 'node:fs/promises';
import { fileURLToPath } from 'node:url';
import { resolve, extname } from 'node:path';
const { chromium } = await import(process.env.PLAYWRIGHT_MODULE || 'playwright');

const dist = fileURLToPath(new URL('../web/desktop/dist/', import.meta.url));
const errors = [];
const json = (res, status, body) => { res.writeHead(status, {'Content-Type': 'application/json'}); res.end(JSON.stringify(body)); };

let hostState = {configured: false, running: false, message: 'No one chat host found in ~/.config/onechat. Run onechat serve.'};
const sockets = new Set();
const requests = [];
let running = null;
const now = () => new Date().toISOString();
const sessions = [
  {id: 'local-invoice', location: 'local', setupId: 'local-claude', harness: 'claudeCode', title: 'Invoice CLI', summary: 'Go invoice tool', state: 'idle', createdAt: now(), updatedAt: now(), workdir: '/home/me/invoice-cli'},
  {id: 'cloud-lisbon', location: 'cloud', setupId: 'cloud-agy', harness: 'agy', title: 'Lisbon trip research', summary: 'Neighbourhoods', state: 'idle', createdAt: now(), updatedAt: now(), namespace: 'anvilhub'},
];
const pastTurn = {turnId: '11111111-1111-4111-8111-111111111111', at: '2026-10-26T20:00:00Z', message: 'which neighbourhood was best for nightlife?', answer: 'Bairro Alto is best for nightlife.', actionCount: 1};
const pastTrace = {id: pastTurn.turnId, at: pastTurn.at, message: pastTurn.message, inventory: sessions,
  decision: {reasoning_summary: 'The Lisbon research already answered this.', actions: [{type: 'read_status', session_id: 'cloud-lisbon'}]},
  actions: [{action: {type: 'read_status', session_id: 'cloud-lisbon'}, sessionId: 'cloud-lisbon', reply: '{"title":"Lisbon trip research"}', state: 'idle', harness: 'agy'}],
  answer: pastTurn.answer};
const history = [pastTurn];

// Minimal RFC 6455 server side: browsers mask every frame they send.
const frame = text => {
  const payload = Buffer.from(text);
  const length = payload.length;
  const header = length < 126 ? Buffer.from([0x81, length]) : length < 65536 ? Buffer.from([0x81, 126, length >> 8, length & 255]) : null;
  return Buffer.concat([header, payload]);
};
const send = (socket, value) => socket.write(frame(JSON.stringify(value)));
const broadcast = value => { for (const socket of sockets) send(socket, value); };
const statusBody = () => ({state: running ? 'busy' : 'idle', turn: running ? {turnId: running.turnId, text: running.text, startedAt: running.startedAt, requestId: running.requestId, by: {name: 'Anvil Agents Desktop', kind: 'desktop'}, events: running.events} : null});
const turnEvent = (name, body) => {
  running.seq += 1;
  const event = {type: 'event', event: name, turnId: running.turnId, seq: running.seq, at: now(), body};
  running.events.push(event);
  broadcast(event);
};

function handleRequest(socket, request) {
  requests.push(request);
  const reply = (body, error) => send(socket, error ? {type: 'response', id: request.id, ok: false, error} : {type: 'response', id: request.id, ok: true, body});
  switch (request.op) {
    case 'hello': return reply({protocol: 1, host: {id: 'host-1', name: 'wsl-fixture', version: '0.0.0-test'}, status: statusBody()});
    case 'sessions.list': return reply({sessions, refreshedAt: now(), stale: Boolean(running)});
    case 'history.list': return reply({messages: [], turns: history});
    case 'trace.get': return request.body.turnId === pastTurn.turnId ? reply({trace: pastTrace}) : reply(null, {code: 'not_found', message: 'No such turn.'});
    case 'turn.start':
      if (running) return reply(null, {code: 'busy', message: 'A turn is running.'});
      running = {turnId: randomUUID(), text: request.body.text, requestId: request.body.requestId, startedAt: now(), events: [], seq: 0, reported: false};
      reply({turnId: running.turnId});
      broadcast({type: 'event', event: 'status', at: now(), body: statusBody()});
      turnEvent('turn.started', {turnId: running.turnId, text: running.text, requestId: running.requestId, by: {name: 'Anvil Agents Desktop', kind: 'desktop'}});
      turnEvent('turn.inventory', {sessions: sessions.map(({id, location, harness, title, state}) => ({id, location, harness, title, state}))});
      turnEvent('turn.decided', {decision: {reasoning_summary: 'The invoice session owns this code.', actions: [{type: 'send', session_id: 'local-invoice', message: 'Add --json'}]}, validationErrors: []});
      turnEvent('action.started', {index: 0, action: {type: 'send', session_id: 'local-invoice'}, sessionId: 'local-invoice', sessionTitle: 'Invoice CLI', harness: 'claudeCode', location: 'local'});
      return;
    case 'turn.cancel':
      if (!running || running.turnId !== request.body.turnId) return reply(null, {code: 'not_found', message: 'Not running.'});
      reply({});
      finishTurn({error: 'send: context canceled'});
      return;
    default: return reply(null, {code: 'invalid', message: `unknown op ${request.op}`});
  }
}

// The session reports back on the running turn's one action.
function report() {
  running.reported = true;
  turnEvent('action.finished', {index: 0, result: {action: {type: 'send', session_id: 'local-invoice'}, sessionId: 'local-invoice', reply: 'Added `--json`; `go test ./...` passes.', state: 'idle', harness: 'claudeCode'}});
}

function finishTurn({error = '', answer = ''}) {
  if (!error) {
    if (!running.reported) report();
    turnEvent('turn.answered', {answer});
  }
  turnEvent('turn.finished', {turnId: running.turnId, error, answer});
  history.push({turnId: running.turnId, at: running.startedAt, message: running.text, answer, ...(error ? {error} : {}), actionCount: 1});
  running = null;
  broadcast({type: 'event', event: 'sessions.changed', at: now(), body: {sessions}});
  broadcast({type: 'event', event: 'status', at: now(), body: statusBody()});
}

function readFrames(socket) {
  let buffer = Buffer.alloc(0);
  socket.on('data', chunk => {
    buffer = Buffer.concat([buffer, chunk]);
    for (;;) {
      if (buffer.length < 2) return;
      const opcode = buffer[0] & 0x0f;
      let length = buffer[1] & 0x7f;
      let offset = 2;
      if (length === 126) { if (buffer.length < 4) return; length = buffer.readUInt16BE(2); offset = 4; }
      if (!(buffer[1] & 0x80)) { errors.push('browser sent an unmasked frame'); socket.destroy(); return; }
      if (buffer.length < offset + 4 + length) return;
      const mask = buffer.subarray(offset, offset + 4);
      const payload = Buffer.from(buffer.subarray(offset + 4, offset + 4 + length));
      for (let i = 0; i < payload.length; i++) payload[i] ^= mask[i % 4];
      buffer = buffer.subarray(offset + 4 + length);
      if (opcode === 8) { socket.end(); return; }
      if (opcode === 1) handleRequest(socket, JSON.parse(payload.toString()));
    }
  });
}

const server = createServer(async (req, res) => {
  const path = new URL(req.url, 'http://fixture').pathname;
  if (path === '/local/v1/snapshot') return json(res, 200, {productTitle: 'Anvil Agents Desktop', prefs: {apiOrigin: ''}, api: {origin: '', reachable: false}, harnesses: [], wsl: {}, harnessTarget: 'native'});
  if (path === '/local/v1/onechat/status') return json(res, 200, hostState);
  const asset = path.startsWith('/assets/') ? resolve(dist, `.${path}`) : resolve(dist, 'index.html');
  try {
    res.setHeader('Content-Type', ({'.js': 'application/javascript', '.css': 'text/css', '.html': 'text/html', '.woff2': 'font/woff2', '.woff': 'font/woff'})[extname(asset)] || 'application/octet-stream');
    res.end(await readFile(asset));
  } catch { res.writeHead(404).end(); }
});
server.on('upgrade', (req, socket) => {
  if (new URL(req.url, 'http://fixture').pathname !== '/local/v1/onechat/connect' || req.headers['sec-websocket-protocol'] !== 'anvil.onechat.v1') {
    errors.push(`unexpected upgrade ${req.url} ${req.headers['sec-websocket-protocol']}`);
    socket.end('HTTP/1.1 400 Bad Request\r\n\r\n');
    return;
  }
  if (req.headers.authorization) errors.push('the page sent an Authorization header; the host token must stay in the Desktop host');
  const accept = createHash('sha1').update(`${req.headers['sec-websocket-key']}258EAFA5-E914-47DA-95CA-C5AB0DC85B11`).digest('base64');
  socket.write(`HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: ${accept}\r\nSec-WebSocket-Protocol: anvil.onechat.v1\r\n\r\n`);
  sockets.add(socket);
  socket.on('close', () => sockets.delete(socket));
  socket.on('error', () => sockets.delete(socket));
  readFrames(socket);
});
await new Promise(done => server.listen(0, '127.0.0.1', done));
const origin = `http://127.0.0.1:${server.address().port}`;

const browser = await chromium.launch({headless: true, ...(process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE ? {executablePath: process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE} : {})});
const page = await (await browser.newContext({viewport: {width: 1280, height: 820}})).newPage();
page.setDefaultTimeout(10000);
page.on('pageerror', error => errors.push(error.message));
const shot = async name => { if (process.env.MANAGER_SHOTS) await page.screenshot({path: `${process.env.MANAGER_SHOTS}/${name}.png`}); };

try {
  // Without a host, the page says how to start one, then connects on its own.
  await page.goto(`${origin}/manager`);
  await page.getByRole('heading', {name: 'Start the manager'}).waitFor();
  await page.getByText('onechat --config ~/.config/onechat/onechat.yaml serve').waitFor();
  await shot('manager-offline');
  hostState = {configured: true, running: true, listen: '127.0.0.1:1740', hostId: 'host-1', version: '0.0.0-test'};
  await page.getByText('wsl-fixture · onechat 0.0.0-test').waitFor({timeout: 8000});
  await page.locator('.chat-sync-live').waitFor();

  // Past exchanges and their sessions; a trace opens on demand.
  await page.getByText('Bairro Alto is best for nightlife.').waitFor();
  await page.getByText('Invoice CLI', {exact: true}).waitFor();
  await page.getByText('Lisbon trip research', {exact: true}).waitFor();
  await page.getByText(/What the manager did/).click();
  await page.getByText('Decided: The Lisbon research already answered this.').waitFor();
  assert.ok(requests.some(request => request.op === 'trace.get' && request.body.turnId === pastTurn.turnId));

  // A live turn shows each step, then becomes part of the conversation once.
  const rail = page.locator('.manager-rail');
  await page.getByLabel('Message', {exact: true}).fill('add a --json flag to the invoice tool');
  await page.keyboard.press('Enter');
  await rail.getByText('Sent work to Invoice CLI').waitFor();
  await rail.getByRole('status').filter({hasText: 'Working in a session'}).waitFor();
  const start = requests.find(request => request.op === 'turn.start');
  assert.match(start.body.requestId, /^[0-9a-f-]{36}$/);
  assert.equal(await page.getByRole('button', {name: 'Working…'}).isDisabled(), true);
  await shot('manager-running');
  finishTurn({answer: 'Done: the invoice tool prints JSON with --json.'});
  await page.getByText('Done: the invoice tool prints JSON with --json.').waitFor();
  await rail.waitFor({state: 'detached'});
  assert.equal(await page.getByText('Done: the invoice tool prints JSON with --json.').count(), 1);
  assert.equal(await page.getByRole('button', {name: 'Send'}).isDisabled(), true, 'empty composer stays disabled');
  await shot('manager-finished');

  // Cancel stops a running turn; the exchange says so in words, not a Go error.
  await page.getByLabel('Message', {exact: true}).fill('now add a total line');
  await page.keyboard.press('Enter');
  await page.getByRole('button', {name: 'Cancel'}).click();
  await page.locator('.remote-chat-caption', {hasText: 'Cancelled before it finished.'}).waitFor();
  assert.ok(requests.some(request => request.op === 'turn.cancel'));
  await rail.waitFor({state: 'detached'});

  // A drop mid-turn: what happened while away comes back from the host's status.
  await page.getByLabel('Message', {exact: true}).fill('add a currency field');
  await page.keyboard.press('Enter');
  await rail.getByText('Sent work to Invoice CLI').waitFor();
  const hellos = requests.filter(request => request.op === 'hello').length;
  for (const socket of sockets) socket.destroy();
  sockets.clear();
  report();
  await page.locator('.chat-sync-retrying').waitFor();
  await page.locator('.chat-sync-live').waitFor({timeout: 8000});
  assert.equal(requests.filter(request => request.op === 'hello').length, hellos + 1);
  await rail.locator('.manager-step.done', {hasText: 'Sent work to Invoice CLI'}).waitFor();
  await page.getByText('Done: the invoice tool prints JSON with --json.').waitFor();
  finishTurn({answer: 'Added a currency field.'});
  await page.getByText('Added a currency field.').waitFor();
  await shot('manager-reconnected');

  assert.deepEqual(errors, []);
  console.log('PASS manager view: offline guidance, past turns with traces, live steps, one answer per turn, cancel in words, catch-up after a drop mid-turn, no token in the page');
} finally {
  for (const socket of sockets) socket.destroy();
  await browser.close();
  server.close();
}

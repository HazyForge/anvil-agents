#!/usr/bin/env node
// Built UI + real Desktop HTTP routing + harmless native executable fixtures.
// Invoked by TestLocalManagerBrowser; no endpoint interception or model call.
import assert from 'node:assert/strict';
const {chromium} = await import(process.env.PLAYWRIGHT_MODULE || 'playwright');
const origin = process.env.ANVIL_LOCAL_MANAGER_ORIGIN;
assert.ok(origin, 'Run through TestLocalManagerBrowser');
const browser = await chromium.launch({headless: true, ...(process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE ? {executablePath: process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE} : {})});
const page = await browser.newPage({viewport: {width: 1440, height: 1000}});
const errors = [], dispatches = [];
page.on('pageerror', error => errors.push(error.message));
page.on('request', request => { if (new URL(request.url()).pathname === '/local/v1/manager/loop/work') dispatches.push(request.postDataJSON()); });
await page.addInitScript(({launch, camera}) => {
  const key = 'anvil-agents-desktop.local-chat.v1';
  localStorage.setItem('anvil-agents-desktop.chat-location', 'local');
  if (localStorage.getItem(key)) return;
  localStorage.setItem(key, JSON.stringify([
    {id: 'launch', name: 'Launch website', title: 'Launch website', harness: 'prime', workdir: launch, target: 'native', wslDistro: '', connectAnvil: false, draft: 'Keep this separate Chat draft', touchedAt: new Date().toISOString(), messages: [{role: 'user', content: 'Original launch brief: build our launch website.'}, {role: 'assistant', content: 'The launch landing page is ready for a contact form.'}]},
    {id: 'camera', name: 'Camera research', title: 'Camera research', harness: 'codex', workdir: camera, target: 'native', wslDistro: '', connectAnvil: false, draft: '', touchedAt: new Date(Date.now() - 86400000).toISOString(), messages: [{role: 'user', content: 'Original camera brief: evaluate local camera tracking.'}, {role: 'assistant', content: 'Camera research is ready for the next tracking example.'}]},
  ]));
}, {launch: process.env.ANVIL_LOCAL_MANAGER_LAUNCH_DIR, camera: process.env.ANVIL_LOCAL_MANAGER_CAMERA_DIR});

async function observed(predicate, argument) {
  const deadline = Date.now() + 30000;
  while (Date.now() < deadline) {
    const result = await page.evaluate(predicate, argument);
    if (result) return result;
    await new Promise(resolve => setTimeout(resolve, 100));
  }
  throw new Error('Timed out waiting for persisted browser evidence');
}

async function route(content) {
  await page.getByRole('textbox', {name: 'Message the manager', exact: true}).fill(content);
  const responsePromise = page.waitForResponse(response => new URL(response.url()).pathname === '/local/v1/manager/route');
  await page.getByRole('button', {name: 'Route message', exact: true}).click();
  const response = await responsePromise;
  assert.equal(response.status(), 200);
  // Chromium may discard a consumed SSE response body; inspect the real
  // application's persisted receipt instead of intercepting the transport.
  const saved = await observed(message => Object.keys(localStorage)
    .filter(key => key.startsWith('anvil-agents-desktop.local-manager.receipt.v1.'))
    .map(key => JSON.parse(localStorage.getItem(key)))
    .find(value => value.content === message && value.result), content);
  assert.deepEqual(saved.events.slice(0, 4).map(event => event.step), ['inventory', 'classifying', 'decision', 'action']);
  return saved.result;
}

try {
  await page.goto(`${origin}/local-manager`);
  await page.getByRole('heading', {name: 'Local Manager', exact: true}).waitFor();
  assert.equal(dispatches.length, 0, 'Opening the manager must not execute a harness');

  // A browser quota failure after completion must not discard the reply or
  // invent a new run. This exercises the shared repository through its real UI.
  await page.evaluate(() => {
    const original = Storage.prototype.setItem;
    window.__managerQuota = true; window.__managerQuotaFailures = 0;
    Storage.prototype.setItem = function(key, value) {
      if (window.__managerQuota && key === 'anvil-agents-desktop.local-chat.v1' && value.includes('Launch fixture finished in its own folder.')) {
        window.__managerQuotaFailures++;
        throw new DOMException('Fixture quota exceeded', 'QuotaExceededError');
      }
      return original.call(this, key, value);
    };
  });

  const launch = await route('Add a contact page to the launch website');
  assert.equal(launch.action.kind, 'continue');
  assert.equal(launch.action.targetId, 'launch');
  const launchReceipt = await observed(() => Object.keys(localStorage)
    .filter(key => key.startsWith('anvil-agents-desktop.local-manager.receipt.v1.'))
    .map(key => JSON.parse(localStorage.getItem(key)))
    .find(value => value.content === 'Add a contact page to the launch website' && (value.reply || value.error)));
  assert.equal(launchReceipt.error, undefined, launchReceipt.error);
  assert.equal(await page.evaluate(() => JSON.parse(localStorage.getItem('anvil-agents-desktop.local-chat.v1')).find(c => c.id === 'launch').draft), 'Keep this separate Chat draft', 'A manager message must preserve the target conversation’s unrelated draft');
  await page.locator('.manager-assistant-message').getByText('Launch fixture finished in its own folder.', {exact: true}).first().waitFor();
  assert.equal(dispatches.length, 1);
  assert.equal(dispatches[0].conversation.harness, 'prime');
  assert.equal(dispatches[0].conversation.workdir, process.env.ANVIL_LOCAL_MANAGER_LAUNCH_DIR);
  await observed(() => window.__managerQuotaFailures > 0);
  const retained = await route('How is the launch website going?');
  assert.equal(retained.action.kind, 'read_status');
  const retainedReceipt = await observed(() => Object.keys(localStorage)
    .filter(key => key.startsWith('anvil-agents-desktop.local-manager.receipt.v1.'))
    .map(key => JSON.parse(localStorage.getItem(key)))
    .find(value => value.content === 'How is the launch website going?' && value.reply));
  assert.ok(retainedReceipt.reply.includes('Launch fixture finished in its own folder.'), 'Status must read the retained unsaved reply, not stale browser storage');
  assert.equal(dispatches.length, 1, 'Storage recovery must not replay work');
  await page.evaluate(() => {window.__managerQuota = false;});

  const status = await route('How is camera research going?');
  assert.equal(status.action.kind, 'read_status');
  assert.equal(status.action.targetId, 'camera');
  await page.getByRole('button', {name: 'Route message', exact: true}).waitFor();
  assert.equal(dispatches.length, 1, 'A saved-status request must not invoke a harness');

  const camera = await route('Add a tracking example to camera research');
  assert.equal(camera.action.kind, 'continue');
  assert.equal(camera.action.targetId, 'camera');
  await page.locator('.manager-assistant-message').getByText('Camera fixture finished in its own folder.', {exact: true}).first().waitFor();
  assert.equal(dispatches.length, 2);
  assert.equal(dispatches[1].conversation.harness, 'codex');
  assert.equal(dispatches[1].conversation.workdir, process.env.ANVIL_LOCAL_MANAGER_CAMERA_DIR);
  const saved = await page.evaluate(() => JSON.parse(localStorage.getItem('anvil-agents-desktop.local-chat.v1')));
  assert.ok(saved.find(item => item.id === 'launch').messages.some(message => message.content === 'Launch fixture finished in its own folder.'));
  assert.ok(saved.find(item => item.id === 'camera').messages.some(message => message.content === 'Camera fixture finished in its own folder.'));
  assert.ok(saved.every(item => !item.pending));

  await page.reload();
  await page.getByRole('heading', {name: 'Local Manager', exact: true}).waitFor();
  await page.locator('.manager-assistant-message').getByText('Camera fixture finished in its own folder.', {exact: true}).first().waitFor();
  assert.equal(dispatches.length, 2, 'Reload must not replay work');
  await page.screenshot({path: process.env.ANVIL_LOCAL_MANAGER_SCREENSHOT || '/tmp/anvil-local-manager-live.png', fullPage: true});

  await page.goto(`${origin}/local-manager/scenarios`);
  const benchResponse = page.waitForResponse(response => new URL(response.url()).pathname === '/local/v1/manager/bench' && response.request().method() === 'POST');
  await page.getByRole('button', {name: 'Run all scenarios', exact: true}).click();
  assert.equal((await benchResponse).status(), 200);
  await new Promise(resolve => setTimeout(resolve, 200));
  assert.deepEqual(errors, [], 'The real benchmark response must render without errors');
  await page.getByRole('heading', {name: /Fixture replay ·/}).waitFor();
  assert.ok(await page.getByRole('region', {name: 'Scenario results'}).getByRole('table').count() > 0);
  assert.equal(dispatches.length, 2, 'Scenario replay must not execute a harness');
  await page.screenshot({path: '/tmp/anvil-local-manager-scenarios.png'});

  // Direct Chat reads the same repository and replies, rather than a copy owned
  // by the manager. This is the wrapper-continuity contract.
  await page.goto(`${origin}/chat`);
  await page.getByRole('button', {name: 'Open Camera research', exact: true}).click();
  await page.locator('.chat-messages').getByText('Camera fixture finished in its own folder.', {exact: true}).first().waitFor();
  await page.goto(`${origin}/local-manager`);
  await page.setViewportSize({width: 390, height: 844});
  await page.getByRole('heading', {name: 'Local Manager', exact: true}).waitFor();
  assert.equal(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth + 1), true, 'Narrow viewport must not overflow horizontally');
  assert.deepEqual(errors, []);
  console.log('PASS real local manager routing and host admission, two harness bindings/folders, isolated saved history, quota-failure retention/recovery, status without execution, shared Chat history, reload without replay, and narrow viewport');
} finally {
  await browser.close();
}

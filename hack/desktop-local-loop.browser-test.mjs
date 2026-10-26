#!/usr/bin/env node
// Actual built UI, Desktop host, durable store, and harmless native fixtures.
// Run through TestLocalManagerLoopBrowser. No fabricated endpoint responses or
// model calls; one real accepted answer response is dropped to test recovery.
import assert from 'node:assert/strict';
import {readFile, realpath, unlink} from 'node:fs/promises';
import {join} from 'node:path';

const {chromium} = await import(process.env.PLAYWRIGHT_MODULE || 'playwright');
const origin = process.env.ANVIL_LOCAL_LOOP_ORIGIN;
const failureDir = process.env.ANVIL_LOCAL_LOOP_FAILURE_DIR;
const sharedDir = process.env.ANVIL_LOCAL_LOOP_SHARED_DIR;
assert.ok(origin && failureDir && sharedDir, 'Run through TestLocalManagerLoopBrowser');
const browser = await chromium.launch({headless: true, ...(process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE ? {executablePath: process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE} : {})});
const context = await browser.newContext({viewport: {width: 1440, height: 1000}});
const errors = [];
context.on('page', page => page.on('pageerror', error => errors.push(error.message)));
await context.addInitScript(({failureDir, sharedDir}) => {
  const key = 'anvil-agents-desktop.local-chat.v1';
  localStorage.setItem('anvil-agents-desktop.chat-location', 'local');
  if (localStorage.getItem('anvil-loop-browser-fixture-seeded')) return;
  localStorage.setItem('anvil-loop-browser-fixture-seeded', 'true');
  const conversation = (id, name, workdir, history) => ({id, name, title: name, harness: 'prime', workdir, target: 'native', wslDistro: '', connectAnvil: false, draft: `Unrelated draft for ${id}`, touchedAt: new Date().toISOString(), messages: [{role: 'user', content: history}, {role: 'assistant', content: `${name} is ready.`}]});
  localStorage.setItem(key, JSON.stringify([
    conversation('failure', 'Parser recovery', failureDir, 'Original parser history: repair a parser.'),
    conversation('gated', 'Gated build', sharedDir, 'Original gated history: run the staged build.'),
    conversation('queued', 'Queued checks', sharedDir, 'Original queued history: check the shared workspace.'),
  ]));
}, {failureDir, sharedDir});

const delay = ms => new Promise(resolve => setTimeout(resolve, ms));
async function snapshot() {
  const response = await fetch(`${origin}/local/v1/manager/loop`);
  assert.equal(response.status, 200);
  return response.json();
}
async function until(read, predicate, description) {
  const deadline = Date.now() + 30000;
  let value;
  while (Date.now() < deadline) {
    value = await read();
    if (predicate(value)) return value;
    await delay(100);
  }
  throw new Error(`Timed out: ${description}; last value: ${JSON.stringify(value)}`);
}
const taskWithContent = (state, content) => state.tasks.find(task => task.content === content);
async function route(page, content) {
  await page.getByRole('textbox', {name: 'Message the manager', exact: true}).fill(content);
  const response = page.waitForResponse(response => new URL(response.url()).pathname === '/local/v1/manager/route');
  await page.getByRole('button', {name: 'Route message', exact: true}).click();
  assert.equal((await response).status(), 200);
}
async function calls(dir) {
  try { return (await readFile(join(dir, 'invocations.txt'), 'utf8')).trim().split('\n').filter(Boolean).length; }
  catch (error) { if (error.code === 'ENOENT') return 0; throw error; }
}

try {
  let page = await context.newPage();
  await page.goto(`${origin}/local-manager`);
  await page.getByRole('heading', {name: 'Local Manager', exact: true}).waitFor();
  await page.getByRole('region', {name: 'Manager loop', exact: true}).waitFor();
  assert.equal(await calls(failureDir), 0, 'Opening the manager must not execute a harness');

  const failedContent = 'Finish the failed parser task';
  await route(page, failedContent);
  let state = await until(snapshot, value => taskWithContent(value, failedContent)?.state === 'needs_decision', 'failed turn becomes a durable question');
  const failedTask = taskWithContent(state, failedContent);
  const question = state.decisions.find(item => item.taskId === failedTask.id && item.state === 'pending');
  assert.ok(question, 'Native failure must create one pending decision');
  assert.equal(failedTask.attempt, 1);
  assert.equal(await calls(failureDir), 1, 'Failure must not silently retry');
  const retryLabel = question.choices.find(choice => choice.id === 'retry')?.label;
  assert.ok(retryLabel, 'Known stopped failure must offer explicit retry');
  await page.locator(`[data-task-id="${failedTask.id}"]`).getByRole('button', {name: retryLabel, exact: true}).waitFor();
  await page.screenshot({path: '/tmp/anvil-local-loop-ask.png', fullPage: true});

  await page.reload();
  await page.getByRole('region', {name: 'Manager loop', exact: true}).waitFor();
  state = await snapshot();
  const pending = state.decisions.filter(item => item.taskId === failedTask.id && item.state === 'pending');
  assert.equal(pending.length, 1, 'Repeated observation must not duplicate the question');
  assert.equal(pending[0].id, question.id, 'Question identity must survive browser reload');
  assert.equal(pending[0].version, question.version);
  assert.equal(await calls(failureDir), 1);
  // Reply through the central manager composer. The decide route must answer
  // the saved interaction instead of creating an ordinary native chat turn.
  // Lose only the browser response after the real host durably accepts it.
  // The retry is recovered with its saved identity after reloading the UI.
  let loseAnswerResponse = true;
  let acceptedAnswer;
  await context.route('**/local/v1/manager/loop/decisions/*/answer', async transport => {
    if (!loseAnswerResponse) {await transport.continue(); return;}
    loseAnswerResponse = false;
    const response = await transport.fetch();
    assert.equal(response.status(), 200);
    acceptedAnswer = transport.request().postDataJSON();
    const result = await response.json();
    assert.equal(result.resolution.choice, 'retry');
    await response.dispose();
    await transport.abort('connectionfailed');
  });
  await route(page, 'retry it');
  state = await until(snapshot, value => taskWithContent(value, failedContent)?.state === 'completed', 'explicit retry is verified');
  const recovered = taskWithContent(state, failedContent);
  assert.equal(recovered.id, failedTask.id);
  assert.equal(recovered.attempt, 2);
  assert.equal(recovered.exitCode, 0);
  assert.equal(recovered.reply, 'Fixture turn completed with a public reply.');
  assert.equal(state.decisions.find(item => item.id === question.id)?.choice, 'retry');
  assert.equal(state.tasks.filter(task => task.content === 'retry it').length, 0, 'A decision answer must not be dispatched as ordinary work');
  assert.equal(await calls(failureDir), 2);
  assert.ok(acceptedAnswer?.requestId, 'The response-loss fault must follow actual host acceptance');
  await page.reload();
  await page.getByRole('region', {name: 'Manager loop', exact: true}).waitFor();
  const replayResponse = page.waitForResponse(response => new URL(response.url()).pathname.endsWith(`/decisions/${question.id}/answer`));
  await page.getByRole('button', {name: 'Recover saved decision', exact: true}).click();
  const replay = await replayResponse;
  assert.equal(replay.status(), 200);
  assert.deepEqual(replay.request().postDataJSON(), acceptedAnswer, 'Answer recovery must preserve its original request ID, version, and content');
  assert.equal((await replay.json()).resolution.source, 'recorded');
  state = await snapshot();
  assert.equal(state.tasks.find(task => task.id === failedTask.id)?.attempt, 2);
  assert.equal(await calls(failureDir), 2, 'Recovering a lost decision response must not start another native attempt');
  const recoveredCard = page.locator(`[data-task-id="${failedTask.id}"]`);
  await recoveredCard.getByText('Observed steps · attempt 2', {exact: true}).click();
  await recoveredCard.getByText(recovered.reply, {exact: true}).waitFor();
  const retryHistory = await page.evaluate(() => JSON.parse(localStorage.getItem('anvil-agents-desktop.local-chat.v1')).find(conversation => conversation.id === 'failure').messages);
  assert.equal(retryHistory.some(message => message.role === 'user' && message.content === 'retry it'), false, 'The decision response belongs to the manager record, not the native transcript');
  assert.equal(await page.evaluate(() => JSON.parse(localStorage.getItem('anvil-agents-desktop.local-chat.v1')).find(conversation => conversation.id === 'failure').workdir), await realpath(failureDir), 'A confirmed submission must project the host canonical folder without stranding its conversation');

  const gatedContent = 'Run the gated build task';
  await route(page, gatedContent);
  state = await until(snapshot, value => taskWithContent(value, gatedContent)?.state === 'running', 'gated native process starts');
  const gatedTask = taskWithContent(state, gatedContent);
  await until(() => calls(sharedDir), count => count === 1, 'native gate records actual admission');
  const queuedContent = 'Run the queued verification task';
  await route(page, queuedContent);
  await page.getByRole('button', {name: 'Queue this message', exact: true}).click();
  state = await until(snapshot, value => taskWithContent(value, queuedContent)?.state === 'queued', 'follow-up is retained while shared workspace is busy');
  const queuedTask = taskWithContent(state, queuedContent);
  assert.equal(await calls(sharedDir), 1, 'Queued work must not overlap active work in the same folder');
  await page.locator(`[data-task-id="${queuedTask.id}"]`).waitFor();
  await page.screenshot({path: '/tmp/anvil-local-loop-queued.png', fullPage: true});

  await page.close();
  await delay(750);
  state = await snapshot();
  assert.equal(state.tasks.find(task => task.id === gatedTask.id)?.state, 'running', 'Closing the UI must not cancel accepted host work');
  assert.equal(state.tasks.find(task => task.id === queuedTask.id)?.state, 'queued');
  assert.equal(await calls(sharedDir), 1);
  await unlink(join(sharedDir, 'hold'));
  state = await until(snapshot, value => [gatedTask.id, queuedTask.id].every(id => value.tasks.find(task => task.id === id)?.state === 'completed'), 'host verifies work and drains the queue without an open page');
  assert.equal(await calls(sharedDir), 2);
  for (const id of [gatedTask.id, queuedTask.id]) {
    assert.equal(state.tasks.find(task => task.id === id)?.reply, 'Fixture turn completed with a public reply.');
    assert.ok(state.events.some(event => event.taskId === id && event.stage === 'verify'), 'Completion needs host verification evidence');
  }

  page = await context.newPage();
  await page.goto(`${origin}/local-manager`);
  await page.getByRole('region', {name: 'Manager loop', exact: true}).waitFor();
  await page.locator(`[data-task-id="${queuedTask.id}"]`).waitFor();
  await until(() => page.evaluate(() => JSON.parse(localStorage.getItem('anvil-agents-desktop.local-chat.v1'))), conversations => conversations.every(conversation => conversation.messages.some(message => message.role === 'assistant' && message.content === 'Fixture turn completed with a public reply.')), 'host replies project into all original conversations');
  const conversations = await page.evaluate(() => JSON.parse(localStorage.getItem('anvil-agents-desktop.local-chat.v1')));
  for (const conversation of conversations) {
    assert.equal(conversation.messages.filter(message => message.content === 'Fixture turn completed with a public reply.').length, 1, 'Reply projection is idempotent across reopen');
    assert.equal(conversation.draft, `Unrelated draft for ${conversation.id}`, 'Loop projection must preserve unsent Chat drafts');
    assert.equal(Boolean(conversation.pending), false);
  }
  assert.equal(await calls(failureDir), 2);
  assert.equal(await calls(sharedDir), 2, 'Reopening must not replay completed work');
  await page.screenshot({path: '/tmp/anvil-local-loop-complete.png', fullPage: true});
  await page.setViewportSize({width: 390, height: 844});
  assert.equal(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth + 1), true, 'Loop panel must fit a narrow viewport');

  // A new browser registry must use the newest saved host baseline, including
  // direct-Chat turns between two managed turns, without duplicating old work.
  await page.setViewportSize({width: 1440, height: 1000});
  await page.goto(`${origin}/chat`);
  await page.getByRole('button', {name: 'Open Parser recovery', exact: true}).click();
  await page.getByRole('textbox', {name: 'Message', exact: true}).fill('Direct Chat bridge note: preserve this intermediate context.');
  await page.getByRole('button', {name: 'Send', exact: true}).click();
  await until(() => page.evaluate(() => JSON.parse(localStorage.getItem('anvil-agents-desktop.local-chat.v1')).find(conversation => conversation.id === 'failure')), conversation => !conversation.pending && conversation.messages.filter(message => message.content === 'Fixture turn completed with a public reply.').length === 2, 'direct Chat records the intermediate turn');
  assert.equal(await calls(failureDir), 3);
  await page.goto(`${origin}/local-manager`);
  const latestContent = 'Finish the failed parser follow-up task using the bridge note';
  await route(page, latestContent);
  await until(snapshot, value => taskWithContent(value, latestContent)?.state === 'completed', 'later manager turn captures the intermediate direct Chat history');
  const latestConversation = await until(() => page.evaluate(() => JSON.parse(localStorage.getItem('anvil-agents-desktop.local-chat.v1')).find(conversation => conversation.id === 'failure')), conversation => !conversation.pending && conversation.messages.filter(message => message.content === 'Fixture turn completed with a public reply.').length === 3, 'latest manager reply projects before registry removal');
  assert.equal(await calls(failureDir), 4);
  assert.ok(latestConversation.messages.some(message => message.content === 'Direct Chat bridge note: preserve this intermediate context.'));
  await page.evaluate(() => {
    localStorage.removeItem('anvil-agents-desktop.local-chat.v1');
    localStorage.removeItem('anvil-agents-desktop.local-chat.v1.selected');
    for (const key of Object.keys(localStorage)) if (key.startsWith('anvil-agents-desktop.local-manager.receipt.v1.')) localStorage.removeItem(key);
  });
  await page.reload();
  await page.getByRole('region', {name: 'Manager loop', exact: true}).waitFor();
  const restored = await until(() => page.evaluate(() => JSON.parse(localStorage.getItem('anvil-agents-desktop.local-chat.v1') || '[]').find(conversation => conversation.id === 'failure')), conversation => conversation && JSON.stringify(conversation.messages) === JSON.stringify(latestConversation.messages), 'fresh registry recovers newest complete history including intervening direct Chat');
  assert.equal(Boolean(restored.pending), false);
  assert.equal(await calls(failureDir), 4, 'Recovering an empty registry must not replay managed or direct work');
  assert.equal(await calls(sharedDir), 2);
  assert.deepEqual(errors, []);
  console.log('PASS real host: detect failure → durable question → central free-text retry answer → verified public reply; lost answer response recovered after reload without another attempt; shared-folder queue; accepted execution and verification survive closing the UI; reopen projects replies once and preserves drafts; a fresh browser registry recovers latest host history with intervening direct Chat. Harness and routing classifier outputs are explicit fixtures; the decision answer uses the real exact-command resolver.');
} finally {
  await browser.close();
}

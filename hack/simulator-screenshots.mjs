#!/usr/bin/env node
// Screenshot the Anvil Agents Console against a running local simulator
// (hack/simulator.sh up). Signs in through the simulator's one-click test
// issuer, then captures the board, a running run with its live stream, the
// library, profiles and controls.
//
//   node hack/simulator-screenshots.mjs [--out DIR] [--width 1440] [--origin URL]
//
// PLAYWRIGHT_MODULE overrides the Playwright import (as the other browser
// tests do); CHROMIUM_EXECUTABLE points at a specific Chromium build.
import {mkdir} from 'node:fs/promises';
import {resolve} from 'node:path';
import {parseArgs} from 'node:util';

const {chromium} = await import(process.env.PLAYWRIGHT_MODULE || 'playwright');

const {values} = parseArgs({
  options: {
    out: {type: 'string', default: resolve(import.meta.dirname, '../.sim/screenshots')},
    width: {type: 'string', default: '1440'},
    origin: {type: 'string', default: `http://127.0.0.1:${process.env.ANVIL_AGENTS_SIM_API_PORT || '18180'}`},
    namespace: {type: 'string', default: 'agents'},
  },
});
const width = Number(values.width);
await mkdir(values.out, {recursive: true});

const browser = await chromium.launch(process.env.CHROMIUM_EXECUTABLE ? {executablePath: process.env.CHROMIUM_EXECUTABLE} : {});
const page = await browser.newPage({viewport: {width, height: 900}});
const errors = [];
page.on('pageerror', (error) => errors.push(error.message));

async function shot(name) {
  await page.waitForTimeout(1500);
  const path = `${values.out}/${name}-${width}.png`;
  await page.screenshot({path, fullPage: true});
  console.log(path);
}

await page.goto(values.origin + '/');
await page.getByRole('button', {name: /sign in/i}).click();
await page.waitForURL((url) => !url.pathname.startsWith('/auth/'), {timeout: 20000});
await page.waitForLoadState('networkidle');
await shot('board');

// Open the first run that is still working, so the live stream has something to show.
const running = await page.evaluate(async (namespace) => {
  const token = sessionStorage.getItem('anvil-agents-console.accessToken');
  const response = await fetch(`/api/v1/namespaces/${namespace}/agent-runs`, {headers: {Authorization: `Bearer ${token}`}});
  const body = await response.json();
  const items = body.items || body.runs || [];
  const live = items.find((run) => (run.phase || run.status?.phase) === 'Running');
  return live ? live.name || live.metadata?.name : null;
}, values.namespace);
if (running) {
  await page.goto(`${values.origin}/ns/${values.namespace}/runs/${running}`);
  await page.waitForTimeout(6000);
  await shot('run-live');
} else {
  console.warn('no running run found; start a wave with hack/simulator.sh wave');
}

for (const [name, path] of [['library', '/library'], ['profiles', '/profiles'], ['controls', '/controls']]) {
  await page.goto(values.origin + path);
  await page.waitForLoadState('networkidle');
  await shot(name);
}

await browser.close();
if (errors.length) {
  console.error(`page errors:\n${errors.join('\n')}`);
  process.exit(1);
}

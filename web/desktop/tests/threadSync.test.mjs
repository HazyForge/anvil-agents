import test from 'node:test';
import assert from 'node:assert/strict';
import {appendLiveToken, eventsRetryDelay, isNearBottom, mergeThreadDetail, mergeThreadList, pollDelay, threadSignature} from '../src/wrapper/threadSync.ts';

const message = (id, sequence, content, role = 'assistant', metadata) => ({id, threadId: 't1', role, content, sequence, createdAt: '', metadata});
const thread = (messages, turns = [], extra = {}) => ({id: 't1', namespace: 'agents', mode: 'persona', title: 't', createdAt: '', updatedAt: 'u1', createdBy: 'me', messages, turns, ...extra});

test('an unchanged read keeps the shown thread object so nothing re-renders', () => {
  const shown = thread([message('m1', 1, 'hello', 'user'), message('m2', 2, 'hi there')]);
  const fresh = structuredClone(shown);
  assert.equal(mergeThreadDetail(shown, fresh), shown);
});

test('a new message reuses every unchanged message object', () => {
  const first = message('m1', 1, 'hello', 'user');
  const shown = thread([first]);
  const merged = mergeThreadDetail(shown, thread([structuredClone(first), message('m2', 2, 'reply')], [], {updatedAt: 'u2'}));
  assert.notEqual(merged, shown);
  assert.equal(merged.messages[0], first);
  assert.equal(merged.messages[1].content, 'reply');
});

test('repaired content or a turn status change counts as a change', () => {
  const shown = thread([message('m1', 1, 'draft')], [{id: 'turn', runName: 'r', status: 'running'}]);
  const repaired = thread([message('m1', 1, 'final')], [{id: 'turn', runName: 'r', status: 'running'}]);
  assert.notEqual(mergeThreadDetail(shown, repaired).messages[0], shown.messages[0]);
  const finished = thread([message('m1', 1, 'draft')], [{id: 'turn', runName: 'r', status: 'succeeded'}]);
  assert.notEqual(threadSignature(shown), threadSignature(finished));
  assert.notEqual(mergeThreadDetail(shown, finished), shown);
});

test('another thread replaces the shown one outright', () => {
  const shown = thread([message('m1', 1, 'a')]);
  const other = {...thread([message('x', 1, 'b')]), id: 't2'};
  assert.equal(mergeThreadDetail(shown, other), other);
});

test('active turns poll fast; live idle threads only need a slow safety read', () => {
  assert.equal(pollDelay({active: true, live: true, visible: true, failures: 0}), 1500);
  assert.equal(pollDelay({active: false, live: true, visible: true, failures: 0}), 20000);
  assert.equal(pollDelay({active: false, live: false, visible: true, failures: 0}), 2500);
  assert.ok(pollDelay({active: false, live: false, visible: false, failures: 0}) > 2500);
  assert.equal(pollDelay({active: true, live: false, visible: true, failures: 1}), 2500);
  assert.equal(pollDelay({active: true, live: false, visible: true, failures: 9}), 15000);
  assert.equal(eventsRetryDelay(1), 1000);
  assert.equal(eventsRetryDelay(20), 30000);
});

test('live tokens assemble in order, ignore repeats and flag a late join', () => {
  let reply = appendLiveToken(undefined, {turnId: 'turn', seq: 0, token: 'Hel', done: false});
  reply = appendLiveToken(reply, {turnId: 'turn', seq: 1, token: 'lo', done: false});
  reply = appendLiveToken(reply, {turnId: 'turn', seq: 1, token: 'lo', done: false});
  reply = appendLiveToken(reply, {turnId: 'turn', seq: 2, token: '', done: true});
  assert.deepEqual({text: reply.text, gaps: reply.gaps, done: reply.done}, {text: 'Hello', gaps: false, done: true});
  const late = appendLiveToken(undefined, {turnId: 'turn', seq: 4, token: 'world', done: false});
  assert.equal(late.gaps, true);
  const next = appendLiveToken(reply, {turnId: 'other', seq: 0, token: 'New', done: false});
  assert.equal(next.text, 'New');
});

test('the view follows new content only near the bottom', () => {
  assert.equal(isNearBottom(900, 1500, 600), true);
  assert.equal(isNearBottom(850, 1500, 600), true);
  assert.equal(isNearBottom(400, 1500, 600), false);
});

test('thread list refresh keeps older history and avoids churn', () => {
  const previous = [{id: 'a', updatedAt: '1'}, {id: 'old', updatedAt: '0'}];
  assert.equal(mergeThreadList(previous, [{id: 'a', updatedAt: '1'}]), previous);
  const merged = mergeThreadList(previous, [{id: 'new', updatedAt: '2'}, {id: 'a', updatedAt: '3'}]);
  assert.deepEqual(merged.map(item => `${item.id}:${item.updatedAt}`), ['new:2', 'a:3', 'old:0']);
});

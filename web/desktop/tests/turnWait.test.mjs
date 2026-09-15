import test from 'node:test';
import assert from 'node:assert/strict';
import {turnWaitFeedback} from '../src/api/turnWait.ts';

test('queued turns stay neutral and a restored 73-minute queue is explicitly delayed', () => {
  assert.deepEqual(turnWaitFeedback('queued',119,119,false),{tone:'waiting'});
  const delayed=turnWaitFeedback('queued',73*60,73*60,false);
  assert.equal(delayed.tone,'delayed');
  assert.match(delayed.label,/startup is delayed/);
  assert.match(delayed.note,/message is saved/);
  assert.doesNotMatch(delayed.label,/working|failed|stopped|offline/);
});

test('runner Running alone does not prove native work started', () => {
  const setup = turnWaitFeedback('running',180,1,false);
  assert.equal(setup.tone,'delayed');
  assert.match(setup.label,/setup is taking longer/);
  assert.match(setup.note,/remote runner has started/);
  assert.equal(turnWaitFeedback('running',180,1,true).tone,'working');
});

test('a quiet active runner gets uncertainty wording and recovers on observed activity', () => {
  const quiet=turnWaitFeedback('running',600,121,true);
  assert.equal(quiet.tone,'delayed');
  assert.match(quiet.label,/may still be running/);
  assert.doesNotMatch(quiet.label,/startup|failed/);
  assert.equal(turnWaitFeedback('running',600,0,true).tone,'working');
});

test('deferred messages and terminal turns keep their own semantics', () => {
  assert.match(turnWaitFeedback('waiting',600,600,false).label,/message is still waiting/);
  assert.equal(turnWaitFeedback('succeeded',9999,9999,false),undefined);
  assert.equal(turnWaitFeedback('failed',9999,9999,false),undefined);
});

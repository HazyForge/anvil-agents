import test from 'node:test';
import assert from 'node:assert/strict';
import { chatFailureLabel, runnerFailureLabel, runnerStateLabel } from '../src/api/runnerState.ts';

test('startup blockers have recoverable labels without trusting server messages', () => {
  for (const code of ['configuration_unavailable', 'image_unavailable', 'capacity_wait']) {
    const label = runnerStateLabel({code, message: 'secret/private-name token=value'});
    assert.match(label, /Waiting/);
    assert.doesNotMatch(label, /secret|private-name|token/);
  }
  assert.equal(runnerStateLabel({code: 'unknown', message: 'raw data'}), undefined);
  assert.equal(runnerStateLabel(), undefined, 'verified recovery clears the blocker');
});


test('prelaunch tool conflict never claims the harness ran', () => {
  const conditions=[{type:'Ready',status:'False',reason:'ConflictingToolName',message:'private tool config'}];
  assert.equal(runnerFailureLabel({conditions}), 'Harness could not start: conflicting tool configuration');
  assert.equal(runnerFailureLabel({conditions,job:{name:'existing'}}), 'Agent could not finish this turn');
  assert.equal(runnerFailureLabel({conditions:[{type:'Ready',status:'True',reason:'ConflictingToolName'}]}), 'Agent could not finish this turn');
});

test('authentication heading recognizes only API canned failure', () => {
  assert.equal(chatFailureLabel('Hermes cannot authenticate with xAI. Renew authentication for the selected remote Hermes harness with `hermes model`, then start a new turn.'), 'Hermes authentication required for xAI');
  assert.equal(chatFailureLabel('401 Unauthorized private-token'), undefined);
  assert.equal(chatFailureLabel('Hermes cannot authenticate with xAI. private-token'), undefined);
});


test('Hermes credential readiness failure does not overclaim expired authentication', () => {
  assert.equal(chatFailureLabel('Hermes provider authentication or configuration is unavailable. Check the selected remote harness provider setup, then start a new turn.'), 'Hermes provider setup required');
  assert.equal(chatFailureLabel('Hermes provider authentication or configuration is unavailable. raw private error'), undefined);
});

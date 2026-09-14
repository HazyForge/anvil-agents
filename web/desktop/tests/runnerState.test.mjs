import test from 'node:test';
import assert from 'node:assert/strict';
import { runnerStateLabel } from '../src/api/runnerState.ts';

test('startup blockers have recoverable labels without trusting server messages', () => {
  for (const code of ['configuration_unavailable', 'image_unavailable', 'capacity_wait']) {
    const label = runnerStateLabel({code, message: 'secret/private-name token=value'});
    assert.match(label, /Waiting/);
    assert.doesNotMatch(label, /secret|private-name|token/);
  }
  assert.equal(runnerStateLabel({code: 'unknown', message: 'raw data'}), undefined);
  assert.equal(runnerStateLabel(), undefined, 'verified recovery clears the blocker');
});

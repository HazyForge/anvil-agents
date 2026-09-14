import assert from 'node:assert/strict';
import test from 'node:test';
import {buildHarnessSpec, formFromHarnessSpec, BACKEND_KIND_OPTIONS} from '../src/pages/library/harnessForm.ts';

test('Prime guided model edits preserve native provider, thinking, session and argv choices', () => {
  const original = {backend: {kind: 'primeAgent', primeAgent: {
    provider: 'deepseek', model: 'deepseek-v4-flash', thinking: 'high', mode: 'json', noSession: true, additionalArgs: ['--tools', 'ipython'],
  }}};
  const form = formFromHarnessSpec(original, 'desktop-prime');
  assert.equal(form.backendKind, 'primeAgent');
  assert.equal(form.model, 'deepseek-v4-flash');
  form.model = 'another-model';
  assert.deepEqual(buildHarnessSpec(form).backend.primeAgent, {...original.backend.primeAgent, model: 'another-model'});
  form.model = '';
  assert.equal(buildHarnessSpec(form).backend.primeAgent.model, undefined);
  assert.equal(original.backend.primeAgent.model, 'deepseek-v4-flash');
  assert.equal(BACKEND_KIND_OPTIONS.find(item => item.kind === 'primeAgent')?.title, 'Prime Agent');
});

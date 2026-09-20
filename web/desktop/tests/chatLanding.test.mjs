import test from 'node:test';
import assert from 'node:assert/strict';
import {preferredStandingChatProfile, shouldRestoreStandingThread} from '../src/wrapper/chatLanding.ts';

const profile = (name, labels = {}) => ({metadata: {name, labels}});

test('prefers desktop-standing-assistant over a project manager', () => {
  const standing = profile('desktop-standing-assistant', {'control.anvil.hazyforge.io/standing': 'true'});
  const manager = profile('hazy-trade-agent-manager', {'control.anvil.hazyforge.io/chat-role': 'project-manager'});
  assert.equal(preferredStandingChatProfile([manager, standing])?.metadata.name, 'desktop-standing-assistant');
});

test('falls back to any standing-labeled profile', () => {
  const standing = profile('desktop-standing-grok', {'control.anvil.hazyforge.io/standing': 'true'});
  assert.equal(preferredStandingChatProfile([standing])?.metadata.name, 'desktop-standing-grok');
});

test('does not treat a manager as the standing landing profile', () => {
  assert.equal(preferredStandingChatProfile([profile('hazy-trade-agent-manager')]), undefined);
});

test('restores a saved thread only when it is the standing assistant', () => {
  const standing = profile('desktop-standing-assistant');
  assert.equal(shouldRestoreStandingThread({profileName: 'desktop-standing-assistant'}, standing), true);
  assert.equal(shouldRestoreStandingThread({profileName: 'hazy-trade-agent-manager'}, standing), false);
  assert.equal(shouldRestoreStandingThread(undefined, standing), false);
});

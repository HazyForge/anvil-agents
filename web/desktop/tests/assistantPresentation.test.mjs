import test from 'node:test';
import assert from 'node:assert/strict';
import {splitAssistantPresentation, stripStatusJSON} from '../src/wrapper/assistantPresentation.ts';

const hey = `I'll read the full mounted prompt first so I can identify the actual run objective and constraints.Continuing through the prompt to find the run objective and mounted context.Latest message is just "hey" — I'll record AgentRun progress, reply briefly, and close the turn.ANVIL_AGENT_RUN_STATUS_JSON={"type":"progress","observedAt":"2026-09-20T14:28:57Z","level":"info","summary":"ChatThread turn: latest user message is hey; acknowledging without new investigation."}
hey — I’m here. What do you want to work on?`;

test('splits manager process narration from the visible reply', () => {
  const got = splitAssistantPresentation(hey);
  assert.equal(got.reply, 'hey — I’m here. What do you want to work on?');
  assert.match(got.reasoning, /mounted prompt/);
  assert.doesNotMatch(got.reasoning, /ANVIL_AGENT_RUN_STATUS_JSON=/);
  assert.doesNotMatch(got.reply, /ANVIL_AGENT_RUN_STATUS_JSON=/);
});

test('uses the text after the last status object when there are two', () => {
  const got = splitAssistantPresentation(`I'll read the prompt.
ANVIL_AGENT_RUN_STATUS_JSON={"type":"progress","summary":"reading"}
Latest message is a casual check-in — I'll close the turn.ANVIL_AGENT_RUN_STATUS_JSON={"type":"progress","summary":"ack"}
Doing well — ready when you are. What should we work on?`);
  assert.equal(got.reply, 'Doing well — ready when you are. What should we work on?');
  assert.match(got.reasoning, /casual check-in/);
});

test('leaves a clean standing reply untouched', () => {
  const got = splitAssistantPresentation('Hey. What can I help you with?');
  assert.equal(got.reasoning, '');
  assert.equal(got.reply, 'Hey. What can I help you with?');
});

test('does not hide the only text when status JSON has no trailing reply', () => {
  const got = splitAssistantPresentation('Working on it.ANVIL_AGENT_RUN_STATUS_JSON={"type":"progress","summary":"x"}');
  assert.equal(got.reasoning, '');
  assert.equal(got.reply, 'Working on it.');
});

test('stripStatusJSON removes inline protocol objects', () => {
  assert.equal(stripStatusJSON('before ANVIL_AGENT_RUN_STATUS_JSON={"type":"progress","summary":"x"} after'), 'before  after');
});

import assert from 'node:assert/strict';
import test from 'node:test';
import {activityFromLog} from '../src/api/runActivity.ts';

const from = value => activityFromLog(JSON.stringify(value));

test('runner setup and status report stages expose only fixed labels', () => {
  const first = activityFromLog('ANVIL_AGENT_RUN_TOOL_VERIFY_START name=secret-path');
  assert.equal(first.label, 'Checking tools');
  assert.deepEqual(first, activityFromLog('ANVIL_AGENT_RUN_TOOL_VERIFY_START name=another-tool'));
  assert.equal(activityFromLog('ANVIL_AGENT_RUN_START backend=agy name=private').label, 'Starting harness');
  assert.equal(activityFromLog('ANVIL_AGENT_RUN_STATUS_JSON={"type":"progress","stage":"inspect-source","summary":"private reasoning","detail":"credential"}').label, 'Inspecting source');
  assert.equal(activityFromLog('ANVIL_AGENT_RUN_STATUS_JSON={"type":"progress","stage":"arbitrary secret","summary":"private"}'), null);
  assert.equal(activityFromLog('ANVIL_AGENT_RUN_STATUS_JSON={"type":"complete","summary":"private"}').label, 'Finished');
});

test('actual entrypoint setup sequence cannot regress after tools are ready', () => {
  const lines = [
    'ANVIL_AGENT_RUN_TOOL_SETUP_START',
    'ANVIL_AGENT_RUN_STATUS_JSON={"type":"progress","stage":"tool-setup","summary":"Preparing AgentRun tools."}',
    'ANVIL_AGENT_RUN_TOOL_VERIFY_START name=git',
    'ANVIL_AGENT_RUN_TOOL_VERIFY_OK name=git',
    'ANVIL_AGENT_RUN_TOOL_SETUP_COMPLETE',
    'ANVIL_AGENT_RUN_STATUS_JSON={"type":"progress","stage":"tool-setup","summary":"AgentRun tools are ready."}',
    'ANVIL_AGENT_RUN_STATUS_JSON={"type":"progress","stage":"unknown","summary":"private"}',
  ];
  assert.deepEqual(lines.map(activityFromLog).filter(Boolean).map(item => item.label), [
    'Preparing tools', 'Checking tools', 'Tools checked', 'Tools ready',
  ]);
  assert.equal(activityFromLog('ANVIL_AGENT_RUN_STATUS_JSON={"type":"progress","stage":"tool-setup"}'), null);
});

test('lifecycle status reports and banners share replay identities', () => {
  const report = stage => activityFromLog(`ANVIL_AGENT_RUN_STATUS_JSON=${JSON.stringify({type: 'progress', stage})}`);
  assert.deepEqual(report('harness-start'), activityFromLog('ANVIL_AGENT_RUN_START backend=agy'));
  assert.deepEqual(report('harness-complete'), activityFromLog('ANVIL_AGENT_RUN_COMPLETE'));
  assert.deepEqual(activityFromLog('ANVIL_AGENT_RUN_STATUS_JSON={"type":"complete","stage":"finished"}'), report('harness-complete'));
});

test('Codex command lifecycle is observed without revealing command or output', () => {
  const event = {type: 'item.started', item: {id: 'item_1', type: 'command_execution', command: 'cat /private/token', aggregated_output: 'secret'}};
  const running = from(event);
  assert.equal(running.label, 'Running a command');
  assert.deepEqual(running, from({...event, item: {...event.item, command: 'different'}}));
  const finished = from({...event, type: 'item.completed'});
  assert.equal(finished.label, 'Command finished');
  assert.notEqual(running.key, finished.key);
  assert.notEqual(running.key, from({...event, item: {...event.item, id: 'item_2'}}).key);
  assert.equal(from({type: 'item.completed', item: {id: 'item_3', type: 'agent_message', text: '{"reply":"secret","messages":[]}'}}).label, 'Composing answer');
});

test('native public tool names are bounded and mapped without inspecting arguments', () => {
  assert.equal(from({type: 'tool_use', part: {id: 'a', tool: 'read', state: {input: {filePath: '/secret'}}}}).label, 'Reading files');
  assert.equal(from({type: 'item.started', item: {type: 'mcp_tool_call', id: 'b', tool: 'search_docs', arguments: 'secret'}}).label, 'Using tool search_docs');
  assert.equal(from({type: 'assistant', message: {content: [{type: 'thinking', text: 'secret'}, {type: 'tool_use', id: 'c', name: 'bash', input: 'secret'}]}}).label, 'Running a command');
  for (const name of ['cat /secret', 'token=abcd', '<script>', 'a'.repeat(49), '\u001b[31mread']) {
    assert.equal(from({type: 'tool_use', part: {tool: name}}).label, 'Using a tool');
  }
});

test('AGY, OpenCode and Grok replies report activity without echoing text', () => {
  assert.equal(from({event: 'step_update', step_update: {step_type: 'agent_response', text_delta: 'private'}}).label, 'Composing answer');
  assert.equal(from({event: 'result', result: {status: 'SUCCESS', response: 'private'}}).label, 'Finished');
  assert.equal(from({type: 'text', part: {text: 'private'}}).label, 'Composing answer');
  assert.equal(from({type: 'assistant', message: {content: [{type: 'thinking', text: 'secret'}, {type: 'text', text: 'private'}]}}).label, 'Composing answer');
  assert.equal(from({text: 'private', stopReason: 'end_turn'}).label, 'Finished');
  assert.equal(from({type: 'result', result: 'private'}).label, 'Finished');
  assert.equal(from({type: 'result', is_error: true, result: 'private credentials'}).label, 'Harness failed');
});

test('unknown logs, reasoning, tool results and malformed frames never become activity', () => {
  for (const line of ['', '{', 'null', '[]', '401 secret-token', 'prefix ANVIL_AGENT_RUN_START', 'ANVIL_AGENT_RUN_START_SUFFIX', 'x'.repeat(256 * 1024 + 1)]) assert.equal(activityFromLog(line), null);
  for (const event of [
    {type: 'item.completed', item: {type: 'reasoning', text: 'secret'}},
    {type: 'assistant', message: {content: [{type: 'thinking', text: 'secret'}]}},
    {event: 'step_update', step_update: {step_type: 'agent_thought', text_delta: 'secret'}},
    {type: 'tool_result', response: 'secret'},
    {reply: 'secret', messages: [{profileName: 'peer', content: 'secret'}]},
    {type: 'unknown', text: 'secret'},
  ]) assert.equal(from(event), null);
});

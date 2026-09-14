export type RunActivity = {
  key: string;
  label: string;
  kind: 'setup' | 'work' | 'reply' | 'error';
};

type RecordValue = Record<string, unknown>;
const record = (value: unknown): RecordValue =>
  value !== null && typeof value === 'object' && !Array.isArray(value) ? value as RecordValue : {};
const text = (value: unknown): boolean => typeof value === 'string' && value.trim().length > 0;
const identifier = (value: unknown): string =>
  typeof value === 'string' && /^[A-Za-z0-9_.:-]{1,96}$/.test(value) ? value : '';

function activity(label: string, kind: RunActivity['kind'], id: unknown = ''): RunActivity {
  return {key: `${identifier(id)}:${kind}:${label}`, label, kind};
}

function toolActivity(name: unknown, id: unknown): RunActivity {
  // Only a short tool name can enter the label. Never inspect arguments, paths,
  // command text, results, model reasoning, or status summary/detail fields.
  const safeName = typeof name === 'string' && /^[A-Za-z][A-Za-z0-9_.-]{0,47}$/.test(name) ? name : '';
  if (safeName === 'ipython') return activity('Running Python', 'work', id);
  if (/^(read|read_file|readfile|view_file)$/i.test(safeName)) return activity('Reading files', 'work', id);
  if (/^(write|write_file)$/i.test(safeName)) return activity('Writing files', 'work', id);
  if (/^(edit|apply_patch)$/i.test(safeName)) return activity('Editing files', 'work', id);
  if (/^(glob|grep)$/i.test(safeName)) return activity('Searching files', 'work', id);
  if (/^(webfetch|web_fetch)$/i.test(safeName)) return activity('Reading a web page', 'work', id);
  if (/^(websearch|web_search)$/i.test(safeName)) return activity('Searching the web', 'work', id);
  if (/^(bash|shell|execute|exec_command|run_command)$/i.test(safeName)) return activity('Running a command', 'work', id);
  return activity(safeName ? `Using tool ${safeName}` : 'Using a tool', 'work', id);
}

const banners: Record<string, [string, RunActivity['kind']]> = {
  TOOL_SETUP_START: ['Preparing tools', 'setup'],
  TOOL_SETUP_SOURCE: ['Preparing tools', 'setup'],
  TOOL_SETUP_COMPLETE: ['Tools ready', 'setup'],
  TOOL_VERIFY_START: ['Checking tools', 'setup'],
  TOOL_VERIFY_OK: ['Tools checked', 'setup'],
  TOOL_VERIFY_SKIPPED: ['Tool checks skipped', 'setup'],
  TOOL_SETUP_MISSING: ['Tool setup failed', 'error'],
  REPO_CLONE: ['Preparing repository', 'setup'],
  REPO_CLONE_FAILED: ['Repository preparation failed', 'error'],
  REPO_CHECKOUT_FAILED: ['Repository preparation failed', 'error'],
  REPO_FETCH_FAILED: ['Repository preparation failed', 'error'],
  GITHUB_AUTH_LOGIN: ['Connecting repository credentials', 'setup'],
  GITHUB_AUTH_TIMEOUT: ['Repository credential setup timed out', 'setup'],
  GITHUB_AUTH_SKIPPED: ['Repository credential setup skipped', 'setup'],
  START: ['Starting harness', 'setup'],
  COMPLETE: ['Finished', 'reply'],
};

/** Normalize one complete runner log line into observed, public activity only.
 * Unknown/plain logs intentionally produce nothing. Output is never reply text.
 * Stable keys let the consumer deduplicate replayed logs and streamed deltas.
 */
export function activityFromLog(line: string): RunActivity | null {
  if (line.length > 256 * 1024) return null;
  const trimmed = line.trim();
  const marker = /^ANVIL_AGENT_RUN_([A-Z_]+)(?:\s|$)/.exec(trimmed);
  if (marker && Object.hasOwn(banners, marker[1])) {
    const [label, kind] = banners[marker[1]];
    return activity(label, kind, `banner.${marker[1]}`);
  }
  const statusPrefix = 'ANVIL_AGENT_RUN_STATUS_JSON=';
  const status = trimmed.startsWith(statusPrefix);
  let parsed: unknown;
  try { parsed = JSON.parse(status ? trimmed.slice(statusPrefix.length) : trimmed); }
  catch { return null; }
  const event = record(parsed);
  if (status) {
    if (event.type === 'failure') return activity('Harness failed', 'error', 'status.failure');
    if (event.type === 'complete') return activity('Finished', 'reply', 'banner.COMPLETE');
    if (event.type === 'needsHuman' || event.needsHuman === true) return activity('Needs your attention', 'error', 'status.needsHuman');
    if (event.type !== 'progress') return null;
    switch (event.stage) {
      // Entrypoints emit this same stage both before setup and after the
      // TOOL_SETUP_COMPLETE banner. Its stage alone cannot identify progress.
      // Trust the explicit banners instead of reading free-form summaries.
      case 'tool-setup': return null;
      case 'harness-start': return activity('Starting harness', 'setup', 'banner.START');
      case 'harness-complete': return activity('Finished', 'reply', 'banner.COMPLETE');
      case 'inspect-source': return activity('Inspecting source', 'work', 'status.inspect-source');
      case 'poll': return activity('Checking for updates', 'work', 'status.poll');
      default: return null;
    }
  }

  // Prime Agent v0.9.4 native session events (docs/json.md). IPython's
  // args/results can contain commands, credentials and model reasoning; only
  // the observed public tool name is eligible for an activity label.
  if (event.type === 'agent_start' || event.type === 'turn_start') return activity('Harness is working', 'work', event.id);
  if (event.type === 'message_start' && record(event.message).role === 'assistant') return activity('Waiting for model output', 'work', record(event.message).id);
  if (event.type === 'tool_execution_start') return toolActivity(event.toolName, event.toolCallId);
  if (event.type === 'tool_execution_end') return activity(event.isError === true ? 'Tool reported an error' : 'Tool finished', event.isError === true ? 'error' : 'work', event.toolCallId);
  if (event.type === 'message_update' || event.type === 'message_end') {
    const message = record(event.message);
    if (message.role !== 'assistant') return null;
    if (event.type === 'message_end' && (message.stopReason === 'error' || message.stopReason === 'aborted')) return activity('Harness failed', 'error', message.id);
    if (event.type === 'message_update' && record(event.assistantMessageEvent).type !== 'text_delta') return null;
    if (Array.isArray(message.content) && message.content.some(block => record(block).type === 'text' && text(record(block).text))) {
      return activity('Composing answer', 'reply', message.id);
    }
    return null;
  }

  // Codex JSONL lifecycle and public item types. Reasoning items are excluded.
  const item = record(event.item);
  if (event.type === 'turn.started') return activity('Harness is working', 'work', event.id);
  if (event.type === 'turn.completed') return activity('Finished', 'reply', event.id);
  if (event.type === 'turn.failed') return activity('Harness failed', 'error', event.id);
  if (event.type === 'item.started' || event.type === 'item.updated' || event.type === 'item.completed') {
    if (item.type === 'command_execution') return activity(event.type === 'item.completed' ? 'Command finished' : 'Running a command', 'work', item.id);
    if (item.type === 'mcp_tool_call') return toolActivity(item.tool, item.id);
    if (item.type === 'agent_message' && text(item.text)) return activity('Composing answer', 'reply', item.id);
    return null;
  }

  // OpenCode run --format json: public tool and text parts.
  const part = record(event.part);
  if (event.type === 'tool_use') return toolActivity(part.tool, part.id);
  if (event.type === 'text' && text(part.text)) return activity('Composing answer', 'reply', part.id);

  // AGY stream-json uses event, step_update and result, not Codex item events.
  const step = record(event.step_update);
  if (event.event === 'step_update' && step.step_type === 'agent_response' && text(step.text_delta)) {
    return activity('Composing answer', 'reply', step.step_id);
  }
  if (event.event === 'result' && record(event.result).status === 'SUCCESS') return activity('Finished', 'reply', event.id);

  // Grok assistant content includes separate thinking blocks: only public text
  // and tool-use block types can signal activity. Never copy their content.
  if (event.type === 'assistant') {
    const message = record(event.message);
    const content = message.content;
    if (Array.isArray(content)) {
      const tool = content.map(record).find(block => block.type === 'tool_use');
      if (tool) return toolActivity(tool.name, tool.id);
      if (content.some(block => record(block).type === 'text' && text(record(block).text))) return activity('Composing answer', 'reply', message.id);
    } else if (text(content)) return activity('Composing answer', 'reply', message.id);
  }
  if (event.type === 'result' && event.is_error === true) return activity('Harness failed', 'error', event.id);
  if (event.type === 'result' && text(event.result)) return activity('Finished', 'reply', event.id);
  if (event.type === undefined && event.event === undefined && event.stopReason === 'end_turn' && text(event.text)) return activity('Finished', 'reply', event.id);
  return null;
}

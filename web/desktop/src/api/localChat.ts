import { parseSSEChunk } from './stream';

export type LocalChatEvent = {anvilConnected?: boolean; remoteNamespace?: string; action?: string; status?: string; line?: string; workdir?: string; target?: string; wslDistro?: string; stdout?: string; stderr?: string; exitCode?: number; timedOut?: boolean; message?: string; stdoutTruncated?: boolean; stdoutEventsDropped?: boolean};

// An HTTP rejection confirms that this request never entered the stream.
// Transport failures after admission provide no such execution guarantee.
export class LocalChatRequestError extends Error {}

export async function streamLocalChat(body: {harness: string; prompt: string; workdir?: string; remoteNamespace?: string}, signal: AbortSignal, onEvent: (event: string, data: LocalChatEvent) => void, accessToken?: string) {
  const response = await fetch('/local/v1/chat/stream', {method: 'POST', headers: {'Content-Type': 'application/json', ...(body.remoteNamespace && accessToken ? {Authorization: `Bearer ${accessToken}`} : {})}, body: JSON.stringify({...body, timeoutSeconds: 300}), signal});
  if (!response.ok) {
    let message = `Local harness request failed (${response.status})`;
    try { const error = await response.json(); message = error.message || message; } catch { /* Keep status. */ }
    throw new LocalChatRequestError(message);
  }
  if (!response.body) throw new Error('The local connection has no response stream. Completion was not confirmed.');
  const reader = response.body.getReader(), decoder = new TextDecoder();
  let buffer = '';
  try {
    while (true) {
      const next = await reader.read();
      if (next.done) break;
      buffer += decoder.decode(next.value, {stream: true});
      if (buffer.length > 4 * 1024 * 1024) throw new Error('The local harness response exceeded the stream limit. Check its work before retrying.');
      const frames = buffer.split(/\r?\n\r?\n/); buffer = frames.pop() || '';
      for (const frame of frames) {
        const parsed = parseSSEChunk(frame);
        if (!parsed) continue;
        const data = JSON.parse(parsed.data) as LocalChatEvent;
        onEvent(parsed.event || '', data);
        if (parsed.event === 'result' || parsed.event === 'error') return;
      }
    }
    throw new Error('The local connection ended before completion was confirmed. Check the harness before retrying.');
  } finally { await reader.cancel().catch(() => undefined); reader.releaseLock(); }
}

const textBlocks = (content: unknown): string => typeof content === 'string' ? content : Array.isArray(content) ? content.filter(b => b?.type === 'text' && typeof b.text === 'string').map(b => b.text).join('\n') : '';
const nativeTextBlocks = (content: unknown): string => Array.isArray(content) && content.every(b => b && typeof b === 'object' && !Array.isArray(b) && typeof b.type === 'string' && (b.text === undefined || typeof b.text === 'string')) ? textBlocks(content) : '';
const nativeText = (value: unknown): string => typeof value === 'string' ? value : '';

/** Read only public assistant envelopes; never turn thinking/tool output into replies. */
export function localReply(stdout: string, harness: string): string {
  const parts: string[] = []; let final = '', structured = false, failed = false;
  for (const line of stdout.split('\n')) {
    let e;
    try { e = JSON.parse(line); } catch { continue; }
    if (!e || typeof e !== 'object') continue;
    structured = true;
    if (harness === 'prime' || harness === 'pi') {
      if (e.type === 'message_end' && e.message?.role === 'assistant') {
        failed = ['error', 'aborted'].includes(e.message.stopReason);
        final = ['stop', 'length'].includes(e.message.stopReason) ? nativeTextBlocks(e.message.content) : '';
      }
    } else if (harness === 'codex') {
      if (e.type === 'turn.failed') failed = true;
      if (e.type === 'item.completed' && e.item?.type === 'agent_message') parts.push(nativeText(e.item.text));
    } else if (harness === 'opencode') {
      if (e.type === 'error') failed = true;
      if (e.type === 'text') parts.push(nativeText(e.part?.text));
    } else if (harness === 'agy' && e.event === 'result') {
      failed = e.result?.status !== 'SUCCESS';
      final = !failed ? nativeText(e.result?.response) : '';
    }
    else if (harness !== 'codex' && harness !== 'opencode' && harness !== 'agy') {
      if (e.type === 'assistant') parts.push(textBlocks(e.message?.content));
      if (e.type === 'result' && e.is_error) failed = true;
      if (e.type === 'result' && typeof e.result === 'string' && !e.is_error) final = e.result;
      if (!e.type && e.stopReason === 'end_turn') final = nativeText(e.text);
    }
  }
  if (failed) return '';
  return final.trim() || parts.join('\n').trim() || (!structured && !['prime', 'pi', 'agy'].includes(harness) ? stdout.trim() : '');
}

/** Prefer a complete capture, including an unterminated final native line.
 * Once that capture truncates, only the streamed tail can contain the answer;
 * never fall back to an earlier reply when native events were dropped.
 */
export function completedLocalReply(streamedOutput: string, result: LocalChatEvent, harness: string): {reply: string; error?: string} {
  const captured = result.stdout || '';
  const truncated = result.stdoutTruncated || new TextEncoder().encode(captured).length >= 256 * 1024;
  if (result.stdoutEventsDropped) {
    return {reply: '', error: 'The local harness output was incomplete. Check its working folder and native session before sending more work.'};
  }
  return {reply: localReply(truncated ? streamedOutput : captured || streamedOutput, harness)};
}

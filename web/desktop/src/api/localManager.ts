import { parseSSEChunk } from './stream';
import type { SubjectDecision } from './subjectRouting';

export type LocalDescriptor = { id: string; label: string; summary: string; harness: string; target: string; wslDistro?: string; status: 'ready' | 'running' | 'unconfirmed'; touchedAt?: string };
export type LocalManagerRequest = { content: string; conversations: LocalDescriptor[]; context: { role: 'user' | 'assistant'; content: string; conversationId?: string; at?: string }[]; targetId?: string; defaultHarness?: string };
export type LocalRouteResult = { requestId?: string; snapshot?: LocalManagerRequest; runtime?: Record<string, unknown>; decision: SubjectDecision; action: { kind: 'continue' | 'read_status' | 'new_conversation' | 'general' | 'wait' | 'clarify'; targetId?: string; reason: string }; candidates: { id: string; label: string; summary: string; kind?: string; ageSeconds?: number }[]; latencyMs: number };
export type LocalRouteEvent = { step: string; at: string; data: Record<string, unknown> };
export type Expectations = { targets: string[]; intents: string[]; actions: string[] };
export type Scenario = { id: string; name: string; description?: string; fixtureOnly?: boolean; runtime: Record<string, unknown>; defaultHarness: string; conversations: LocalDescriptor[]; context?: LocalManagerRequest['context']; turns: { id: string; content: string; targetId?: string; expect: Expectations }[] };
export type BenchTurn = { id: string; content: string; expected: Expectations; actualTarget: string; actualIntent?: string; judgmentSource?: string; skipped?: boolean; targetPass: boolean; intentPass: boolean; actionPass: boolean; passed: boolean; error?: string; result: LocalRouteResult; snapshot: LocalManagerRequest; runtime: Record<string, unknown> };
export type BenchResult = { mode: 'fixture' | 'live'; complete: boolean; errorReason?: string; provenance: string; startedAt: string; scenarios: { id: string; name: string; turns: BenchTurn[] }[]; totals: { turns: number; passed: number; targetPassed: number; intentPassed: number; actionPassed: number; manualTurns?: number; providerTurns?: number } };
export type LocalManagerAvailability = { enabled: boolean; available: boolean; reason?: string };
async function checked(response: Response) {
  if (!response.ok) {
    let message = `Local manager request failed (${response.status}).`;
    try { const body = await response.json(); message = body.message || body.error || message; } catch { /* Retain HTTP status. */ }
    throw new Error(message);
  }
  return response;
}
export async function routeLocalMessage(request: LocalManagerRequest, signal: AbortSignal, onEvent: (event: LocalRouteEvent) => void): Promise<LocalRouteResult> {
  const response = await checked(await fetch('/local/v1/manager/route', { method: 'POST', headers: { 'Content-Type': 'application/json', Accept: 'text/event-stream' }, body: JSON.stringify(request), signal }));
  if (!response.headers.get('content-type')?.includes('text/event-stream')) return response.json();
  if (!response.body) throw new Error('Routing returned no stream. No local message was sent.');
  const reader = response.body.getReader(), decoder = new TextDecoder();
  let buffer = '', result: LocalRouteResult | undefined;
  const consume = (frame: string) => {
    const event = parseSSEChunk(frame);
    if (!event?.data) return;
    const data = JSON.parse(event.data);
    if (event.event === 'error') throw new Error(data.message || data.error || 'Routing stopped. No local message was sent.');
    if (event.event === 'result') result = data;
    else onEvent({ step: event.event || 'update', at: new Date().toISOString(), data });
  };
  try {
    while (true) {
      const next = await reader.read();
      buffer += decoder.decode(next.value, { stream: !next.done });
      if (buffer.length > 2 * 1024 * 1024) throw new Error('Routing exceeded the response limit. No local message was sent.');
      const frames = buffer.split(/\r?\n\r?\n/); buffer = frames.pop() || '';
      frames.forEach(consume);
      if (next.done) break;
    }
    if (buffer.trim()) consume(buffer);
  } finally { await reader.cancel().catch(() => undefined); reader.releaseLock(); }
  if (!result) throw new Error('Routing ended before a final decision. No local message was sent. Route again when ready.');
  return result;
}
export async function loadLocalScenarios(signal?: AbortSignal): Promise<{ scenarios: Scenario[]; localManager: LocalManagerAvailability }> {
  return (await checked(await fetch('/local/v1/manager/bench', { signal }))).json();
}
export async function runLocalScenarios(mode: 'fixture' | 'live', scenarioIds?: string[], scenarios?: Scenario[], signal?: AbortSignal): Promise<BenchResult> {
  return (await checked(await fetch('/local/v1/manager/bench', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ mode, scenarioIds, scenarios }), signal }))).json();
}

import type { OnechatAction, OnechatActionResult, OnechatDecision, OnechatEvent, OnechatRunningTurn } from '../api/onechat';

/**
 * Fold protocol v1 turn events into what the Manager view shows for one turn:
 * the message, the decision, each session's action and the answer. Pure so the
 * node test runner can load it directly.
 */
export type TurnActionView = {
  index: number;
  action: OnechatAction;
  sessionId?: string;
  sessionTitle?: string;
  harness?: string;
  location?: string;
  status: 'running' | 'done' | 'failed';
  result?: OnechatActionResult;
};

export type TurnView = {
  turnId: string;
  text: string;
  requestId?: string;
  by?: {name?: string; kind?: string};
  startedAt?: string;
  sessionCount?: number;
  decision?: OnechatDecision;
  validationErrors: string[];
  actions: TurnActionView[];
  answer?: string;
  finished: boolean;
  error?: string;
  lastSeq: number;
};

const text = (value: unknown) => (typeof value === 'string' ? value : '');

export function emptyTurn(turnId: string, message = ''): TurnView {
  return {turnId, text: message, validationErrors: [], actions: [], finished: false, lastSeq: 0};
}

/** Apply one event. Events for another turn, or already applied (by seq), change nothing. */
export function applyTurnEvent(view: TurnView | undefined, event: OnechatEvent): TurnView | undefined {
  if (!event.turnId) return view;
  const current = view && view.turnId === event.turnId ? view : emptyTurn(event.turnId);
  if (typeof event.seq === 'number' && event.seq <= current.lastSeq) return view && view.turnId === event.turnId ? view : current;
  const next: TurnView = {...current, actions: current.actions, lastSeq: typeof event.seq === 'number' ? event.seq : current.lastSeq};
  const body = event.body ?? {};
  switch (event.event) {
    case 'turn.started':
      next.text = text(body.text) || next.text;
      next.requestId = text(body.requestId) || next.requestId;
      next.by = (body.by as TurnView['by']) ?? next.by;
      next.startedAt = event.at;
      break;
    case 'turn.inventory':
      next.sessionCount = Array.isArray(body.sessions) ? body.sessions.length : next.sessionCount;
      break;
    case 'turn.decided':
      next.decision = (body.decision as OnechatDecision) ?? next.decision;
      next.validationErrors = Array.isArray(body.validationErrors) ? (body.validationErrors as string[]) : [];
      break;
    case 'action.started': {
      const index = Number(body.index);
      next.actions = [...next.actions.filter(item => item.index !== index), {
        index,
        action: (body.action as OnechatAction) ?? {type: 'unknown'},
        sessionId: text(body.sessionId) || undefined,
        sessionTitle: text(body.sessionTitle) || undefined,
        harness: text(body.harness) || undefined,
        location: text(body.location) || undefined,
        status: 'running' as const,
      }].sort((a, b) => a.index - b.index);
      break;
    }
    case 'action.finished': {
      const index = Number(body.index);
      const result = body.result as OnechatActionResult | undefined;
      const prior = next.actions.find(item => item.index === index);
      const finished: TurnActionView = {
        ...(prior ?? {index, action: result?.action ?? {type: 'unknown'}}),
        status: result?.error || result?.state === 'failed' ? 'failed' : 'done',
        result,
        sessionId: prior?.sessionId ?? result?.sessionId,
      };
      next.actions = [...next.actions.filter(item => item.index !== index), finished].sort((a, b) => a.index - b.index);
      break;
    }
    case 'turn.answered':
      next.answer = text(body.answer) || next.answer;
      break;
    case 'turn.finished':
      next.finished = true;
      next.answer = text(body.answer) || next.answer;
      next.error = text(body.error) || undefined;
      // An action that never reported back was cut short (a cancel or a failure).
      next.actions = next.actions.map(item => item.status === 'running' ? {...item, status: 'failed' as const} : item);
      break;
    default:
      return view && view.turnId === event.turnId ? view : current;
  }
  return next;
}

/** Rebuild the running turn from a status snapshot (hello, status, reconnect). */
export function turnFromStatus(turn: OnechatRunningTurn | null | undefined): TurnView | undefined {
  if (!turn) return undefined;
  let view: TurnView | undefined = {...emptyTurn(turn.turnId, turn.text), requestId: turn.requestId, by: turn.by, startedAt: turn.startedAt};
  for (const event of turn.events ?? []) view = applyTurnEvent(view, event);
  return view;
}

/** Why a turn stopped, in words; Go reports a cancel or a timeout as a context error. */
export function stopReason(error: string): string {
  if (/\bcontext canceled\b/i.test(error)) return 'Cancelled before it finished.';
  if (/\bcontext deadline exceeded\b/i.test(error)) return 'Ran out of time before it finished.';
  return `Stopped: ${error}`;
}

const intentLabels: Record<string, string> = {
  new_work: 'New work',
  amend_running: 'More for a session',
  status: 'Status',
  question: 'Question',
  decide: 'Your answer',
  stop: 'Stop',
  preference: 'A rule',
  chatter: 'Chat',
  unclear: 'Unclear',
};

/** How the manager read the message, in words; empty when it gave no intent. */
export function intentLabel(intent?: string): string {
  return intent ? intentLabels[intent] ?? intent : '';
}

/** Plain-language label for an action, from the person's side of the screen. */
export function actionLabel(item: Pick<TurnActionView, 'action' | 'sessionTitle'>): string {
  const target = item.sessionTitle || item.action.title || item.action.session_id || item.action.setup_id || 'a session';
  switch (item.action.type) {
    case 'send': return `Sent work to ${target}`;
    case 'create': return `Started ${item.action.title || 'a new session'}`;
    case 'read_status': return `Checked ${target}`;
    case 'answer': return 'Answered directly';
    case 'ask_user': return 'Asked you a question';
    default: return item.action.type;
  }
}

type TraceLike = {
  id: string;
  at?: string;
  message?: string;
  inventory?: {id: string; title?: string; harness?: string; location?: string}[];
  decision?: OnechatDecision;
  validationErrors?: string[];
  actions?: OnechatActionResult[];
  answer?: string;
  error?: string;
};

/** Rebuild a finished turn from its saved trace (`trace.get`). */
export function turnFromTrace(trace: TraceLike): TurnView {
  const titles = new Map((trace.inventory ?? []).map(session => [session.id, session]));
  return {
    ...emptyTurn(trace.id, trace.message ?? ''),
    startedAt: trace.at,
    sessionCount: trace.inventory?.length,
    decision: trace.decision,
    validationErrors: trace.validationErrors ?? [],
    actions: (trace.actions ?? []).map((result, index) => {
      const session = titles.get(result.sessionId);
      return {
        index,
        action: result.action,
        sessionId: result.sessionId || undefined,
        sessionTitle: session?.title,
        harness: result.harness || session?.harness,
        location: session?.location,
        status: result.error || result.state === 'failed' ? 'failed' : 'done',
        result,
      };
    }),
    answer: trace.answer,
    error: trace.error || undefined,
    finished: true,
  };
}

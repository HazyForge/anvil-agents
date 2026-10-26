import type { ChatMessage } from '../api/types.chat';
import type { RemoteThreadDetail, RemoteTurn } from '../api/remoteChat';

/**
 * Pure helpers that keep an open remote conversation current without
 * re-rendering it when nothing changed. Kept free of runtime imports so the
 * node test runner can load them directly.
 */

const turnKey = (turn: RemoteTurn) => [turn.id, turn.status, turn.retryCount ?? 0, turn.runName, turn.error ?? '', turn.retryAt ?? '',
  turn.attempts?.length ?? 0, (turn.delegates ?? []).map(d => `${d.turnId}:${d.status}`).join(',')].join('|');

const messageKey = (message: ChatMessage) => `${message.id}|${message.sequence}|${message.role}|${JSON.stringify(message.metadata ?? null)}|${message.content}`;

/** Everything the conversation view shows, as one comparable string. */
export function threadSignature(detail: RemoteThreadDetail): string {
  return [
    detail.id, detail.updatedAt, detail.profileName ?? '', detail.recoveryPending ? 'recovering' : '',
    JSON.stringify(detail.metadata ?? null),
    detail.messages.map(messageKey).join('\u0000'),
    (detail.turns ?? []).map(turnKey).join('\u0000'),
    detail.activeTurn ? turnKey(detail.activeTurn) : '',
  ].join('\u0001');
}

/**
 * Merge a fresh read into the shown thread. Returns `previous` itself when
 * nothing visible changed, and otherwise reuses every unchanged message
 * object so memoized message rows skip re-rendering their Markdown.
 */
export function mergeThreadDetail(previous: RemoteThreadDetail | null, next: RemoteThreadDetail): RemoteThreadDetail {
  if (!previous || previous.id !== next.id) return next;
  if (threadSignature(previous) === threadSignature(next)) return previous;
  const prior = new Map(previous.messages.map(message => [message.id, message]));
  const messages = next.messages.map(message => {
    const before = prior.get(message.id);
    return before && messageKey(before) === messageKey(message) ? before : message;
  });
  return {...next, messages};
}

/** Messages and turns only grow; a read that lost them is stale. */
export function latestSequence(detail: RemoteThreadDetail): number {
  return detail.messages.reduce((max, message) => Math.max(max, message.sequence), 0);
}

export type PollInputs = {
  /** A turn is waiting, queued or running, or a send is unconfirmed. */
  active: boolean;
  /** The namespace events stream is connected and delivering. */
  live: boolean;
  /** The window is visible. */
  visible: boolean;
  /** Consecutive failed reads. */
  failures: number;
};

/**
 * Next thread read. Active turns poll quickly because a read also completes
 * finished Job-plane runs. With live events an idle thread only needs a slow
 * safety-net read; without them the previous 2.5s cadence stays.
 */
export function pollDelay({active, live, visible, failures}: PollInputs): number {
  if (failures > 0) return Math.min(15_000, 2_500 * 2 ** Math.min(failures - 1, 3));
  if (active) return visible ? 1_500 : 3_000;
  if (live) return visible ? 20_000 : 60_000;
  return visible ? 2_500 : 10_000;
}

/** Reconnect delay for the events stream after a failure. */
export function eventsRetryDelay(failures: number): number {
  return Math.min(30_000, 1_000 * 2 ** Math.max(0, Math.min(failures - 1, 5)));
}

export type LiveReply = {turnId: string; text: string; nextSeq: number; gaps: boolean; done: boolean};

/**
 * Fold one token frame into the live reply for its turn. Frames are ordered
 * by seq; a duplicate is ignored, and a frame that arrives after a gap is
 * still appended but marks the text as incomplete (the durable reply that
 * follows replaces it either way).
 */
export function appendLiveToken(current: LiveReply | undefined, frame: {turnId: string; seq: number; token: string; done: boolean}): LiveReply {
  const base = current && current.turnId === frame.turnId ? current : {turnId: frame.turnId, text: '', nextSeq: frame.seq, gaps: frame.seq > 0, done: false};
  if (frame.seq < base.nextSeq) return frame.done && !base.done ? {...base, done: true} : base;
  return {
    turnId: frame.turnId,
    text: base.text + frame.token,
    nextSeq: frame.seq + 1,
    gaps: base.gaps || frame.seq > base.nextSeq,
    done: base.done || frame.done,
  };
}

/** The bottom is "near" within this many pixels, so new content follows it. */
export const PIN_THRESHOLD_PX = 96;

export function isNearBottom(scrollTop: number, scrollHeight: number, clientHeight: number, threshold = PIN_THRESHOLD_PX): boolean {
  return scrollHeight - (scrollTop + clientHeight) <= threshold;
}

type ListedThread = {id: string; updatedAt?: string; title?: string; profileName?: string};

/**
 * Fold a fresh namespace thread list into the shown one. Threads the fresh
 * list omits (older per-agent history) stay; updated threads replace their
 * entries. Returns `previous` when nothing changed.
 */
export function mergeThreadList<T extends ListedThread>(previous: T[], fresh: T[]): T[] {
  const byId = new Map(fresh.map(thread => [thread.id, thread]));
  const key = (thread: T) => `${thread.id}|${thread.updatedAt ?? ''}|${thread.title ?? ''}|${thread.profileName ?? ''}`;
  const known = new Set(previous.map(thread => thread.id));
  const merged = [...fresh.filter(thread => !known.has(thread.id)), ...previous.map(thread => byId.get(thread.id) ?? thread)];
  return merged.length === previous.length && merged.every((thread, index) => key(thread) === key(previous[index])) ? previous : merged;
}

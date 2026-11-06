import {useCallback, useEffect, useRef, useState} from 'react';
import type {Snapshot} from '../api/types';
import {loadManagerLoop, observeManagerConversation, type LoopSnapshot} from '../api/localManagerLoop';
import {loopConversation, projectManagerLoop, readLocalConversations, requiresBrowserRecovery, useLocalConversations} from './localConversations';

export function useLocalManagerLoop(snapshot: Snapshot, observeConversations = false) {
  const registry = useLocalConversations(snapshot);
  const [loop, setLoop] = useState<LoopSnapshot | null>(null);
  const [error, setError] = useState(''), [projectionError, setProjectionError] = useState(''), [observedAt, setObservedAt] = useState('');
  const current = useRef<LoopSnapshot | null>(null), running = useRef(registry.running), importing = useRef(new Set<string>());
  const inFlight = useRef<Promise<void> | null>(null), active = useRef(true);
  running.current = registry.running;
  const accept = useCallback(async (next: LoopSnapshot) => {
    if (!Array.isArray(next.tasks) || !Array.isArray(next.decisions) || !Array.isArray(next.events)) throw new Error('The host did not return a valid manager loop snapshot.');
    if (current.current && current.current.revision > next.revision) return;
    current.current = next;
    const projection = await projectManagerLoop(snapshot,next);
    if (!active.current) return;
    setLoop(next); setProjectionError(projection); setObservedAt(new Date().toISOString()); setError(next.storageError || '');
  },[snapshot]);
  const refresh = useCallback(async () => {
    if (inFlight.current) return inFlight.current;
    const work = (async () => {
      try {
        await accept(await loadManagerLoop());
        if (!observeConversations) return;
        for (const conversation of readLocalConversations(snapshot)) {
          if (conversation.managerTaskId || conversation.managerRequestId || running.current.has(conversation.id) || (!conversation.pending && !conversation.lastError)) continue;
          if (requiresBrowserRecovery(conversation)) continue;
          if (conversation.target !== 'native' && !(conversation.target === 'wsl' && conversation.wslDistro)) continue;
          if (current.current?.tasks.some(task => task.conversation.id === conversation.id && !['completed','cancelled'].includes(task.state))) continue;
          const fingerprint = JSON.stringify([conversation.id,conversation.pending,conversation.lastError,conversation.messages]);
          if (importing.current.has(fingerprint)) continue;
          // Retrying an observation cannot execute a harness; the host also
          // deduplicates its frozen conversation fingerprint across tabs.
          const next = await observeManagerConversation({requestId: crypto.randomUUID(),conversation: loopConversation(conversation),pending: !!conversation.pending,lastError: conversation.lastError});
          importing.current.add(fingerprint);
          await accept(next);
        }
      } catch (err) {if (active.current) setError(err instanceof Error ? err.message : String(err));}
    })();
    inFlight.current = work;
    try {await work;} finally {if (inFlight.current === work) inFlight.current = null;}
  },[accept,snapshot,observeConversations]);
  useEffect(() => {
    active.current = true;
    void refresh();
    const timer = window.setInterval(() => void refresh(),500);
    return () => {active.current = false; window.clearInterval(timer);};
  },[refresh]);
  return {loop, error, projectionError, observedAt, loading: !loop, refresh, accept};
}

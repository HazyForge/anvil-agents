import { useSyncExternalStore } from 'react';
import type { Snapshot } from '../api/types';
import type { LoopConversation, LoopSnapshot } from '../api/localManagerLoop';
import { taskBlocksConversation } from '../api/localManagerLoop';

export type LocalMessage = { role: 'user' | 'assistant'; content: string };
export type LocalConversation = {
  id: string; title: string; name: string; harness: string; workdir: string;
  messages: LocalMessage[]; draft: string; pending?: boolean; lastError?: string;
  target?: string; wslDistro?: string; connectAnvil?: boolean; remoteNamespace?: string;
  createdAt?: string; touchedAt?: string;
  managerKind?: 'general'; managerDefaultWorkdir?: string;
  managerTaskId?: string; managerRequestId?: string; managerAppliedTasks?: string[];
  managerSubmission?: {requestId: string; conversation: LoopConversation; content: string};
  lastTurnAnvilConnected?: boolean;
  nativeSession?: { id: string; harness: string; observedAt: string; source: 'native_event' };
};
export const localConversationStorageKey = 'anvil-agents-desktop.local-chat.v1';
export const localConversationSelectedKey = `${localConversationStorageKey}.selected`;
const draftPrefix = `${localConversationStorageKey}.draft.v1.`;
const harnessNames: Record<string, string> = { prime: 'Prime Agent', codex: 'Codex', opencode: 'OpenCode', agy: 'Antigravity', grok: 'Grok', pi: 'Pi' };
export const localHarnessName = (id: string) => harnessNames[id] || id;
export function localRuntimeIdentity(snapshot: Snapshot) {
  const target = snapshot.harnessTarget === 'wsl' ? 'wsl' : 'native';
  const wslDistro = target === 'wsl' ? (snapshot.wsl.insideWSL ? snapshot.wsl.defaultDistro : snapshot.prefs.wslDistro || snapshot.wsl.defaultDistro) || '' : '';
  return { target, wslDistro };
}

type RepositoryState = { conversations: LocalConversation[]; running: ReadonlySet<string>; storageError: string };
let state: RepositoryState | undefined;
let currentSnapshot: Snapshot;
const admissionLock = 'anvil-agents-desktop.local-conversations';
const executionLock = 'anvil-agents-desktop.local-execution';
let lastStorageRaw: string | null | undefined;
let unsaved = false;
let storageConflict = false;
const ownedRunning = new Set<string>();
const executionReleases = new Map<string, () => void>();
const listeners = new Set<() => void>();
function restore(snapshot: Snapshot, raw: string | null): LocalConversation[] {
  let records: LocalConversation[] = [];
  try {
    const value = JSON.parse(raw || '[]');
    if (Array.isArray(value)) records = value.filter(c => typeof c?.id === 'string' && typeof c.harness === 'string' && typeof c.workdir === 'string' && typeof c.draft === 'string' && (c.lastError === undefined || typeof c.lastError === 'string') && Array.isArray(c.messages) && c.messages.every((m: LocalMessage) => m && ['user', 'assistant'].includes(m.role) && typeof m.content === 'string'));
  } catch { /* Preserve the existing local-only fallback when storage fails. */ }
  const used = new Set(records.map(c => typeof c.name === 'string' ? c.name.trim().toLowerCase() : '').filter(Boolean));
  records = records.map(c => {
    if (typeof c.name === 'string' && c.name.trim()) return { ...c, name: c.name.trim() };
    const base = localHarnessName(c.harness); let name = base, number = 2;
    while (used.has(name.toLowerCase())) name = `${base} ${number++}`;
    used.add(name.toLowerCase());
    return { ...c, name };
  });
  if (!records.some(c => c.harness === 'prime')) records.unshift({ id: 'local-prime-agent', name: 'Prime Agent', title: 'Prime Agent', harness: 'prime', workdir: '', messages: [], draft: '', ...localRuntimeIdentity(snapshot) });
  return records.map(c => ({...c,draft:readDraft(c.id,c.draft)}));
}
function readDraft(id: string, fallback: string): string {
  try {return localStorage.getItem(draftPrefix+id) ?? fallback;} catch {return fallback;}
}
/** Drafts have their own synchronous slot, so a reload immediately after a
 * keystroke cannot outrun the asynchronous lock used for transcript writes. */
export function saveLocalDraft(id: string, draft: string): void {
  try {localStorage.setItem(draftPrefix+id,draft);}
  catch {if (state) publish({...state,storageError:'This draft could not be saved. Keep this tab open until browser storage is available.'});}
}
function clearSubmittedDraft(id: string, content: string) {
  if (readDraft(id,'').trim() === content.trim()) saveLocalDraft(id,'');
}
function initialize(snapshot: Snapshot) {
  currentSnapshot = snapshot;
  if (!state) {
    try {
      lastStorageRaw = localStorage.getItem(localConversationStorageKey);
      state = { conversations: restore(snapshot, lastStorageRaw), running: new Set(), storageError: '' };
    } catch {
      state = { conversations: restore(snapshot, null), running: new Set(), storageError: 'Browser storage is unavailable. Local execution is paused until conversations can be saved.' };
    }
    window.addEventListener('storage', event => {
      if (event.key === localConversationStorageKey || event.key === null) {refreshFromStorage(); void refreshRunningLocks();}
      else if (event.key?.startsWith(draftPrefix) && state) publish({...state,conversations:state.conversations.map(c=>({...c,draft:readDraft(c.id,c.draft)}))});
    });
    // An abandoned page releases its Web Lock without changing localStorage.
    // Refresh pending conversations so another tab can distinguish running from
    // unconfirmed work without assuming a timeout means the process stopped.
    window.setInterval(() => {if (state?.conversations.some(c => c.pending)) void refreshRunningLocks();}, 1500);
    void refreshRunningLocks();
  }
  return state;
}
function publish(next: RepositoryState) {
  state = next;
  for (const listener of listeners) listener();
}
function persist(next: RepositoryState): boolean {
  try {
    if (storageConflict) throw new Error('storage conflict');
    const raw = JSON.stringify(next.conversations);
    localStorage.setItem(localConversationStorageKey, raw);
    lastStorageRaw = raw; unsaved = false;
    publish({...next, storageError: ''});
    return true;
  } catch {
    unsaved = true;
    publish({...next, storageError: storageConflict
      ? 'Another tab changed conversations while this tab could not save. Keep this tab open and copy its unsaved messages before reloading. New execution is paused.'
      : 'Browser storage is full or unavailable. Unsaved messages remain in this tab. Restore storage before sending more work.'});
    return false;
  }
}
export function readLocalConversations(snapshot: Snapshot): LocalConversation[] { initialize(snapshot); refreshFromStorage(); return state!.conversations; }
function refreshFromStorage(): boolean {
  if (!state) return false;
  let raw: string | null;
  try {raw = localStorage.getItem(localConversationStorageKey);}
  catch {
    publish({...state, storageError: 'Browser storage is unavailable. Keep this tab open to retain its messages. New execution is paused.'});
    return false;
  }
  if (unsaved) {
    if (raw !== lastStorageRaw) storageConflict = true;
    if (storageConflict) publish({...state, storageError: 'Another tab changed conversations while this tab could not save. Keep this tab open and copy its unsaved messages before reloading. New execution is paused.'});
    return !storageConflict;
  }
  if (raw !== lastStorageRaw) {
    lastStorageRaw = raw;
    publish({...state, conversations: restore(currentSnapshot, raw), storageError: ''});
  }
  return true;
}
function mutate(transform: (previous: LocalConversation[]) => LocalConversation[]): boolean {
  if (!state) throw new Error('Local conversations have not been initialized.');
  const readable = refreshFromStorage();
  const now = new Date().toISOString();
  const previous = new Map(state.conversations.map(conversation => [conversation.id, conversation]));
  const conversations = transform(state.conversations).map(conversation => {
    const old = previous.get(conversation.id);
    if (!old) return { ...conversation, createdAt: conversation.createdAt || now, touchedAt: conversation.touchedAt || now };
    return old.messages !== conversation.messages || old.pending !== conversation.pending || old.lastError !== conversation.lastError
      ? { ...conversation, touchedAt: now } : conversation;
  });
  if (!readable) {unsaved = true; publish({...state, conversations}); return false;}
  return persist({ ...state, conversations });
}
export function setLocalConversations(transform: (previous: LocalConversation[]) => LocalConversation[]): Promise<void> {
  // Every durable mutation uses the same origin-wide lock as admission.
  if (navigator.locks) return navigator.locks.request(admissionLock, () => {mutate(transform);});
  mutate(transform);
  return Promise.resolve();
}
export function updateLocalConversation(id: string, transform: (conversation: LocalConversation) => LocalConversation) {
  return setLocalConversations(previous => previous.map(conversation => conversation.id === id ? transform(conversation) : conversation));
}
async function refreshRunningLocks() {
  if (!state || !navigator.locks) return;
  try {
    const locks = await navigator.locks.query();
    const held = locks.held?.some(lock => lock.name === executionLock);
    const running = new Set(ownedRunning);
    if (held) for (const conversation of state.conversations) if (conversation.pending) running.add(conversation.id);
    if (running.size !== state.running.size || [...running].some(id => !state!.running.has(id))) publish({...state, running});
  } catch { /* An unavailable lock query never grants execution or clears pending. */ }
}
export function setLocalConversationRunning(id: string, running: boolean) {
  if (!state) return;
  if (running) ownedRunning.add(id);
  else {
    ownedRunning.delete(id);
    const release = executionReleases.get(id); executionReleases.delete(id); release?.();
  }
  const next = new Set(state.running);
  if (running) next.add(id); else next.delete(id);
  publish({ ...state, running: next });
}
export async function acknowledgeLocalConversation(id: string): Promise<void> {
  const managed = state?.conversations.find(c => c.id === id);
  if (managed?.managerTaskId || managed?.managerRequestId) throw new Error('This work is tracked by the manager. Resolve its saved decision in Local Manager.');
  if (!navigator.locks) throw new Error('This browser cannot verify whether another tab is still running local work.');
  await navigator.locks.request(executionLock, {ifAvailable: true}, async lock => {
    if (!lock) throw new Error('Local work is still running in another tab. Wait for completion before clearing this guard.');
    await navigator.locks.request(admissionLock, () => {
      if (!mutate(previous => previous.map(c => c.id === id ? {...c, pending: false, lastError: undefined} : c))) throw new Error('The acknowledgment could not be saved. Restore browser storage before continuing.');
    });
  });
}
export function useLocalConversations(snapshot: Snapshot) {
  initialize(snapshot);
  return useSyncExternalStore(callback => { listeners.add(callback); return () => listeners.delete(callback); }, () => state!, () => state!);
}
export function buildLocalConversationPrompt(conversation: LocalConversation, content: string): string {
  return conversation.messages.length
    ? `Continue this conversation. Prior messages below are context; respond to the latest user message. Use your tools for requested work.\n${JSON.stringify([...conversation.messages, { role: 'user', content }])}`
    : content;
}

/** Provenance only: recording a native ID never enables native session resume. */
export function observeLocalNativeSession(id: string, harness: string, line: string) {
  if (harness !== 'codex') return;
  try {
    const event = JSON.parse(line) as { type?: string; thread_id?: string };
    if (event.type !== 'thread.started' || typeof event.thread_id !== 'string' || !/^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i.test(event.thread_id)) return;
    updateLocalConversation(id, conversation => ({ ...conversation, nativeSession: { id: event.thread_id!, harness, observedAt: new Date().toISOString(), source: 'native_event' } }));
  } catch { /* Non-session native events remain in the normal activity stream. */ }
}

/** Atomically re-read the wrapper binding and transcript before native admission. */
export async function admitLocalTurn(snapshot: Snapshot, expected: LocalConversation, content: string, anvilConnected = false): Promise<{conversation: LocalConversation; prompt: string}> {
  if (!navigator.locks) throw new Error('This browser cannot coordinate local execution across tabs. Use a browser with Web Locks support. Your message was not sent.');
  return new Promise((resolve, reject) => {
    // Hold the execution lease until the stream is settled, not just until its
    // pending marker is saved. Other tabs cannot acknowledge active work.
    void navigator.locks.request(executionLock, {ifAvailable: true}, async lock => {
      if (!lock) {reject(new Error('Local work is still running in another tab. Your message was not sent.')); return;}
      let release!: () => void;
      const held = new Promise<void>(done => {release = done;});
      try {
        const admission = await navigator.locks.request(admissionLock, () => {
          initialize(snapshot);
          if (!refreshFromStorage() || storageConflict) throw new Error('Local conversations could not be refreshed safely. Restore browser storage before sending.');
          if (unsaved && !persist(state!)) throw new Error('Unsaved local messages could not be saved. Restore browser storage before sending.');
          const fresh = state!.conversations.find(c => c.id === expected.id);
          if (!fresh) throw new Error('This conversation is no longer available. Your message was not sent.');
          if (fresh.managerTaskId || fresh.managerRequestId) throw new Error('The manager is still tracking this conversation. Resolve its saved decision in Local Manager before sending new work.');
          if (state!.conversations.some(c => c.pending)) throw new Error('Another local conversation is running or its completion is unconfirmed. Your message was not sent.');
          const binding = (c: LocalConversation) => JSON.stringify([c.harness, c.workdir, c.target, c.wslDistro || '', c.connectAnvil, c.remoteNamespace]);
          if (binding(fresh) !== binding(expected) || JSON.stringify(fresh.messages) !== JSON.stringify(expected.messages)) throw new Error('This conversation changed while the manager was choosing. Review it and route again. Your message was not sent.');
          const runtime = localRuntimeIdentity(snapshot);
          if (fresh.target !== runtime.target || (fresh.wslDistro || '') !== runtime.wslDistro) throw new Error('This conversation belongs to a different runtime. Check its folder in Chat settings before sending.');
          if (!snapshot.harnesses.some(h => h.id === fresh.harness && h.present && h.delegatable)) throw new Error('This conversation’s harness is unavailable. Your message was not sent.');
          const prompt = buildLocalConversationPrompt(fresh, content);
          if (new TextEncoder().encode(prompt).length > 64000) throw new Error('This conversation exceeds the local prompt limit. Its saved history is unchanged. Start a new conversation with a summary.');
          if (!mutate(previous => previous.map(c => c.id === fresh.id ? { ...c, messages: [...c.messages, { role: 'user', content }], draft: c.draft.trim() === content.trim() ? '' : c.draft, pending: true, lastError: undefined, lastTurnAnvilConnected: anvilConnected } : c))) {
            // Admission failed before a process existed. Retain the original
            // draft/history in memory instead of inventing an unconfirmed run.
            publish({...state!, conversations: state!.conversations.map(c => c.id === fresh.id ? fresh : c)});
            throw new Error('The pending turn could not be saved. Restore browser storage before sending local work.');
          }
          executionReleases.set(fresh.id, release);
          clearSubmittedDraft(fresh.id,content);
          setLocalConversationRunning(fresh.id, true);
          return {conversation: fresh, prompt};
        });
        resolve(admission);
        await held;
      } catch (err) {release(); reject(err);}
    }).catch(reject);
  });
}

export function loopConversation(conversation: LocalConversation): LoopConversation {
  return {id: conversation.id, name: conversation.name, harness: conversation.harness, workdir: conversation.workdir,
    target: conversation.target || '', ...(conversation.wslDistro ? {wslDistro: conversation.wslDistro} : {}), messages: conversation.messages};
}
export const requiresBrowserRecovery = (conversation: LocalConversation) => !!conversation.lastTurnAnvilConnected || conversation.connectAnvil === true || !!conversation.remoteNamespace;

/** Save an admission marker before asking the host. A lost HTTP reply retains
 * this identity; it cannot be turned into a fresh execution by reloading. */
export async function prepareManagedTurn(snapshot: Snapshot, expected: LocalConversation, content: string, requestId: string): Promise<LocalConversation> {
  if (!navigator.locks) throw new Error('This browser cannot coordinate local conversation writes.');
  return navigator.locks.request(admissionLock, () => {
    initialize(snapshot);
    if (!refreshFromStorage() || storageConflict || (unsaved && !persist(state!))) throw new Error('Restore browser storage before sending work to the manager.');
    const fresh = state!.conversations.find(c => c.id === expected.id);
    if (!fresh || JSON.stringify(loopConversation(fresh)) !== JSON.stringify(loopConversation(expected))) throw new Error('This conversation changed while routing. Review it before sending.');
    if (fresh.pending || fresh.managerTaskId || fresh.managerRequestId) throw new Error('The manager must finish or resolve this conversation’s current work before accepting another message.');
    if (!mutate(previous => previous.map(c => c.id === fresh.id ? {...c, messages: [...c.messages, {role: 'user', content}], draft: c.draft.trim() === content.trim() ? '' : c.draft, pending: true, lastError: undefined, managerRequestId: requestId, managerSubmission: {requestId,conversation:loopConversation(fresh),content}} : c))) {
      publish({...state!, conversations: state!.conversations.map(c => c.id === fresh.id ? fresh : c)});
      throw new Error('The pending message could not be saved. Restore browser storage before sending.');
    }
    clearSubmittedDraft(fresh.id,content);
    return fresh;
  });
}

export async function rejectManagedTurn(before: LocalConversation, requestId: string): Promise<void> {
  await setLocalConversations(previous => previous.map(c => {
    if (c.id !== before.id || c.managerRequestId !== requestId || c.managerTaskId) return c;
    const draft = c.draft || before.draft;
    saveLocalDraft(c.id,draft);
    return {...before,draft};
  }));
}

/** The host owns results. Apply each result only to its frozen baseline, never
 * overwrite unrelated browser edits or append a duplicate on reconnect. */
export async function projectManagerLoop(snapshot: Snapshot, loop: LoopSnapshot): Promise<string> {
  initialize(snapshot);
  if (!loop.tasks.length) return '';
  let conflict = '';
  const project = () => {
    if (!refreshFromStorage() || storageConflict) return;
    let changed = false;
    const conversations = [...state!.conversations];
    const tasks = [...loop.tasks].sort((a,b) => a.createdAt.localeCompare(b.createdAt));
    const latestByConversation = new Map(tasks.map(task => [task.conversation.id,task]));
    // A new browser has no local history to protect. Its most recent host
    // snapshot includes intervening direct-Chat turns; replaying oldest-first
    // would manufacture a conflict and strand that newer saved transcript.
    for (const latest of latestByConversation.values()) {
      const index = conversations.findIndex(c => c.id === latest.conversation.id);
      const existing = index < 0 ? undefined : conversations[index];
      const emptyDefault = !lastStorageRaw && !unsaved && existing?.id === 'local-prime-agent' && !existing.messages.length && !existing.draft && !existing.workdir;
      if (existing && !emptyDefault) continue;
      const restored: LocalConversation = {...latest.conversation,title:latest.conversation.name,draft:'',connectAnvil:false,
        managerAppliedTasks: tasks.filter(task => task.conversation.id === latest.conversation.id && task.id !== latest.id && ['completed','cancelled'].includes(task.state)).map(task => task.id)};
      if (index < 0) conversations.push(restored); else conversations[index] = restored;
      changed = true;
    }
    for (const task of tasks) {
      const index = conversations.findIndex(c => c.id === task.conversation.id);
      const current = index < 0 ? {...task.conversation, title: task.conversation.name, draft: '', connectAnvil: false} as LocalConversation : conversations[index];
      if (current.managerAppliedTasks?.includes(task.id)) continue;
      const base = task.conversation.messages;
      const admitted = task.content ? [...base, {role: 'user' as const, content: task.content}] : base;
      const completed = task.state === 'completed' && task.reply ? [...admitted, {role: 'assistant' as const, content: task.reply}] : admitted;
      const canonicalizedSubmission = current.managerRequestId === task.requestId && current.managerSubmission?.requestId === task.requestId && current.workdir === current.managerSubmission.conversation.workdir;
      const canonicalizedObservation = task.origin === 'observed' && task.submittedWorkdir !== undefined && current.workdir === task.submittedWorkdir;
      const sameBinding = current.harness === task.conversation.harness && current.target === task.conversation.target && (current.wslDistro || '') === (task.conversation.wslDistro || '') && (current.workdir === task.conversation.workdir || canonicalizedSubmission || canonicalizedObservation);
      const currentMessages = JSON.stringify(current.messages);
      if (!sameBinding || ![base, admitted, completed].some(messages => JSON.stringify(messages) === currentMessages)) {
        conflict = `Saved work for “${task.conversation.name}” differs from this browser’s conversation. Its host record is intact; review the saved reply in Manager before changing this chat.`;
        continue;
      }
      const terminal = task.state === 'completed' || task.state === 'cancelled';
      const next: LocalConversation = {...current, workdir: task.conversation.workdir, messages: completed,
        pending: taskBlocksConversation(task), lastError: terminal ? undefined : task.error,
        managerTaskId: terminal ? undefined : task.id, managerRequestId: terminal ? undefined : task.requestId,
        managerSubmission: terminal ? undefined : current.managerSubmission,
        managerAppliedTasks: terminal ? [...(current.managerAppliedTasks || []),task.id] : current.managerAppliedTasks};
      if (JSON.stringify(next) !== JSON.stringify(current)) {changed = true; if (index < 0) conversations.push(next); else conversations[index] = next;}
    }
    if (changed) mutate(() => conversations);
  };
  if (navigator.locks) await navigator.locks.request(admissionLock, project); else project();
  return conflict;
}

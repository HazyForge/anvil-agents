// A lost HTTP response must not turn a sidebar click or reload into a second
// execution. Keep only the unresolved local send until its receipt is observed.
export type PendingChatSend = {content: string; id: string; threadID: string; preserveDraft?: boolean};
const key = (namespace: string, threadID: string) => `anvil-agents-desktop.pending.${namespace}.${threadID}`;
export function readPendingSend(namespace: string, threadID: string): PendingChatSend | null {
  try { return JSON.parse(sessionStorage.getItem(key(namespace, threadID)) || 'null') as PendingChatSend | null; }
  catch { return null; }
}
export function rememberPendingSend(namespace: string, threadID: string, content: string, options?: {preserveDraft?: boolean; forceNew?: boolean}): PendingChatSend {
  const old = readPendingSend(namespace, threadID);
  if (!options?.forceNew && old?.content === content && old.threadID === threadID) return old;
  const next = {content, threadID, id: crypto.randomUUID(), ...(options?.preserveDraft ? {preserveDraft: true} : {})};
  try { sessionStorage.setItem(key(namespace, threadID), JSON.stringify(next)); } catch { /* memory retry remains available */ }
  return next;
}
export function clearPendingSend(namespace: string, threadID: string) {
  try { sessionStorage.removeItem(key(namespace, threadID)); } catch { /* unavailable storage */ }
}

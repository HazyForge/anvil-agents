// Tab-local drafts are not persisted server messages. Empty thread id denotes
// this namespace's unfinished new conversation; pending sends keep separate IDs.
export type NewChatConfig = {
  profile: string; harness: string; mode: 'persona' | 'fleet'; coordinate: boolean; peers: string[];
};
const key = (kind: string, ns: string, id = '') => `anvil-agents-desktop.chat.${kind}.${JSON.stringify([ns, id])}`;
function read(name: string): string | null { try { return sessionStorage.getItem(name); } catch { return null; } }
function write(name: string, value: string) { try { sessionStorage.setItem(name, value); } catch { /* React state remains available. */ } }
export function readChatDraft(ns: string, id: string) { return read(key('draft', ns, id)); }
export function saveChatDraft(ns: string, id: string, content: string) { write(key('draft', ns, id), content); }
export function readSelectedChat(ns: string) { return read(key('selected', ns)) ?? ''; }
export function saveSelectedChat(ns: string, id: string) { write(key('selected', ns), id); }
export function readNewChatConfig(ns: string): NewChatConfig | null {
  try {
    const value: unknown = JSON.parse(read(key('new-config', ns)) || 'null');
    if (!value || typeof value !== 'object') return null;
    const c = value as NewChatConfig;
    return typeof c.profile === 'string' && typeof c.harness === 'string' &&
      ['persona', 'fleet'].includes(c.mode) && typeof c.coordinate === 'boolean' &&
      Array.isArray(c.peers) && c.peers.every(p => typeof p === 'string') ? c : null;
  } catch { return null; }
}
export function saveNewChatConfig(ns: string, config: NewChatConfig) { write(key('new-config', ns), JSON.stringify(config)); }

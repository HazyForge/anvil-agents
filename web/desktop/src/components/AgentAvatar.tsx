import type { CSSProperties } from 'react';

/** A stable identity mark, never a presence indicator or a provider logo. */
export function AgentAvatar({name, identity = name, size = 'sm'}: {name: string; identity?: string; size?: 'sm' | 'lg'}) {
  const hash = [...identity].reduce((value, char) => ((value * 31) + char.charCodeAt(0)) >>> 0, 0);
  const hues = [151, 185, 215, 267, 36, 330];
  const words = name.replace(/^(desktop|anvil)[-_ ]/i, '').split(/[-_\s]+/).filter(Boolean);
  const initials = (words.length > 1 ? words[0][0] + words[1][0] : words[0]?.slice(0, 2) || 'AG').toUpperCase();
  return <span className={`agent-avatar agent-avatar-${size}`} style={{'--agent-hue': hues[hash % hues.length]} as CSSProperties} aria-hidden="true">
    <svg viewBox="0 0 40 40" fill="none"><path d="M20 3 35 12v16L20 37 5 28V12Z" stroke="currentColor" strokeWidth="1.5"/><path d="m20 3 15 9-15 8L5 12M20 20v17" stroke="currentColor" strokeWidth="1" opacity=".35"/></svg>
    <span>{initials}</span>
  </span>;
}

export function DesktopIcon({kind}: {kind: 'chat' | 'activity' | 'harness' | 'forge' | 'project'}) {
  return <svg className="desktop-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
    {kind === 'project' ? <path d="M3 7V5a1 1 0 0 1 1-1h5l2 3h9a1 1 0 0 1 1 1v11a1 1 0 0 1-1 1H4a1 1 0 0 1-1-1Z"/> : kind === 'chat' ? <><path d="M5 4h14a2 2 0 0 1 2 2v10a2 2 0 0 1-2 2H9l-6 3V6a2 2 0 0 1 2-2Z"/><path d="M8 9h8M8 13h5"/></> : kind === 'activity' ? <><path d="M3 12h4l3-7 4 14 3-7h4"/></> : kind === 'harness' ? <><path d="m8 7-5 5 5 5m8-10 5 5-5 5m-3-13-2 18"/></> : <><path d="M3 5h18v4l-6 3H9L3 9ZM10 12v5l-4 3h12l-4-3v-5"/></>}
  </svg>;
}

import { useId, type CSSProperties } from 'react';

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

export function DesktopIcon({kind}: {kind: 'chat' | 'activity' | 'harness' | 'forge' | 'project' | 'refresh' | 'signin' | 'signout' | 'manager'}) {
  return <svg className="desktop-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
    {kind === 'project' ? <path d="M3 7V5a1 1 0 0 1 1-1h5l2 3h9a1 1 0 0 1 1 1v11a1 1 0 0 1-1 1H4a1 1 0 0 1-1-1Z"/> : kind === 'chat' ? <><path d="M5 4h14a2 2 0 0 1 2 2v10a2 2 0 0 1-2 2H9l-6 3V6a2 2 0 0 1 2-2Z"/><path d="M8 9h8M8 13h5"/></> : kind === 'activity' ? <><path d="M3 12h4l3-7 4 14 3-7h4"/></> : kind === 'harness' ? <><path d="m8 7-5 5 5 5m8-10 5 5-5 5m-3-13-2 18"/></> : kind === 'manager' ? <><circle cx="12" cy="12" r="3"/><circle cx="5" cy="5" r="2"/><circle cx="19" cy="5" r="2"/><circle cx="5" cy="19" r="2"/><circle cx="19" cy="19" r="2"/><path d="m6.5 6.5 3.4 3.4m4.2 0 3.4-3.4M6.5 17.5l3.4-3.4m4.2 0 3.4 3.4"/></> : kind === 'refresh' ? <><path d="M20 11a8 8 0 0 0-14.9-3.5M4 4v4h4M4 13a8 8 0 0 0 14.9 3.5M20 20v-4h-4"/></> : kind === 'signin' ? <><path d="M14 4h4a2 2 0 0 1 2 2v12a2 2 0 0 1-2 2h-4M10 16l4-4-4-4M14 12H4"/></> : kind === 'signout' ? <><path d="M10 4H6a2 2 0 0 0-2 2v12a2 2 0 0 0 2 2h4M16 16l4-4-4-4M20 12H10"/></> : <><path d="M3 5h18v4l-6 3H9L3 9ZM10 12v5l-4 3h12l-4-3v-5"/></>}
  </svg>;
}

/** The canonical Hazy Forge mark (hazyforge.io), filled with the brand gradient. */
export function HazyForgeMark({className = 'brand-mark'}: {className?: string}) {
  const id = `hf-mark-${useId().replace(/[^a-zA-Z0-9_-]/g, '')}`;
  return <svg className={className} viewBox="0 0 160 99.2" aria-hidden="true">
    <defs><linearGradient id={id} x1="34.01" x2="157.7" y1="77.74" y2="12.58" gradientUnits="userSpaceOnUse"><stop offset="0" stopColor="#3786F7"/><stop offset=".4896" stopColor="#50D8FA"/><stop offset="1" stopColor="#E9FDFF"/></linearGradient></defs>
    <path fill={`url(#${id})`} d="m23.5 57.1-9.6 15.8h9.9c2.5 0 6.1-1.6 8.2-5l7.3-10.8h-15.8zm32.2-53.7-21.6 34.1h16.6l21.2-34.7h-16.1l-0.1 0.6zm41.9 9.2c-4.2 0-10.3 2.8-12.7 5.9l-11.2 18.8c-1.2 2.1-4.7 2.8-7.1 2.8h-56l-7.8 14.2 60.6 0.1-22.1 34.4h11.7c4.4 0 7.3-1.7 9.6-5l31.1-52.5c0.9-1.4 4.8-3.9 8-3.9h47.4l8.5-14.8h-60zm-9.9 47.6-21.9 36h12.7l21.9-36h-12.7zm11-17.2-9.1 14.4h36.7c2.2 0 5.9-1.3 7.4-3.6l6.8-10.8h-41.8z"/>
  </svg>;
}

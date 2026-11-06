import type { ReactNode } from "react";

/** Line icons for the Forge Deck console. Decorative: always aria-hidden. */
const PATHS: Record<string, ReactNode> = {
  runs: <path d="M3 12h4l2.5-6.5 5 13L17 12h4" />,
  chat: (
    <>
      <path d="M4 5.5h16v10.5H10l-4.5 3.5V16H4z" />
      <path d="M8 9.5h8M8 12.5h5" />
    </>
  ),
  profiles: (
    <>
      <rect x="3.5" y="4.5" width="17" height="15" rx="1.5" />
      <circle cx="9.5" cy="10.5" r="2.4" />
      <path d="M5.8 16.5c.8-1.9 2.1-2.8 3.7-2.8s2.9.9 3.7 2.8M15 9.5h3M15 13h3" />
    </>
  ),
  library: (
    <>
      <path d="m12 3.5 8.5 4.5-8.5 4.5L3.5 8z" />
      <path d="m3.5 12 8.5 4.5 8.5-4.5M3.5 16l8.5 4.5 8.5-4.5" />
    </>
  ),
  controls: (
    <>
      <path d="M4 7h9M17 7h3M4 12h3M11 12h9M4 17h11M19 17h1" />
      <circle cx="15" cy="7" r="2" />
      <circle cx="9" cy="12" r="2" />
      <circle cx="17" cy="17" r="2" />
    </>
  ),
  harness: (
    <>
      <rect x="6.5" y="6.5" width="11" height="11" rx="1" />
      <rect x="9.5" y="9.5" width="5" height="5" />
      <path d="M9.5 3.5v3M14.5 3.5v3M9.5 17.5v3M14.5 17.5v3M3.5 9.5h3M3.5 14.5h3M17.5 9.5h3M17.5 14.5h3" />
    </>
  ),
  skills: (
    <>
      <path d="M5 4.5h11.5a2 2 0 0 1 2 2v13H7a2 2 0 0 1-2-2z" />
      <path d="M5 17.5a2 2 0 0 1 2-2h11.5M9 8.5h6M9 11.5h4" />
    </>
  ),
  tools: (
    <>
      <path d="m4.5 6.5 5 5.5-5 5.5" />
      <path d="M12 17.5h7.5" />
    </>
  ),
  councils: (
    <>
      <circle cx="9" cy="9" r="3" />
      <circle cx="16.5" cy="10" r="2.4" />
      <path d="M3.5 19c.9-3 3-4.6 5.5-4.6s4.6 1.6 5.5 4.6M14.6 14.6c.6-.2 1.2-.3 1.9-.3 2 0 3.5 1.3 4 3.7" />
    </>
  ),
  volumes: (
    <>
      <ellipse cx="12" cy="6" rx="7.5" ry="2.5" />
      <path d="M4.5 6v12c0 1.4 3.4 2.5 7.5 2.5s7.5-1.1 7.5-2.5V6M4.5 12c0 1.4 3.4 2.5 7.5 2.5s7.5-1.1 7.5-2.5" />
    </>
  ),
  disk: (
    <>
      <path d="M3.5 13.5 6 5h12l2.5 8.5" />
      <rect x="3.5" y="13.5" width="17" height="6" rx="1" />
      <path d="M7 16.5h.01M10 16.5h.01" />
    </>
  ),
  key: (
    <>
      <circle cx="8" cy="15" r="3.5" />
      <path d="m10.5 12.5 8-8M15.5 7.5l2.5 2.5M13.5 9.5l2 2" />
    </>
  ),
  trigger: <path d="M13 3 5 13.5h6L10 21l8-10.5h-6z" />,
  folder: <path d="M3.5 6.5a1 1 0 0 1 1-1h5l2 2h8a1 1 0 0 1 1 1v9.5a1 1 0 0 1-1 1h-15a1 1 0 0 1-1-1z" />,
  chevron: <path d="m9.5 6 6 6-6 6" />,
  close: <path d="M6.5 6.5l11 11M17.5 6.5l-11 11" />,
  logout: (
    <>
      <path d="M14 4.5h4.5a1 1 0 0 1 1 1v13a1 1 0 0 1-1 1H14" />
      <path d="m9.5 8-4 4 4 4M5.5 12H15" />
    </>
  ),
  refresh: (
    <>
      <path d="M19.5 12a7.5 7.5 0 1 1-2.2-5.3" />
      <path d="M19.5 4.5v4h-4" />
    </>
  ),
  external: (
    <>
      <path d="M13.5 4.5h6v6M19.5 4.5l-8.5 8.5" />
      <path d="M17.5 14v5.5h-13v-13H10" />
    </>
  ),
  back: <path d="M19 12H5.5M11 6l-6 6 6 6" />,
  plus: <path d="M12 5v14M5 12h14" />,
  search: (
    <>
      <circle cx="10.5" cy="10.5" r="6" />
      <path d="m15 15 5 5" />
    </>
  ),
  lock: (
    <>
      <rect x="5" y="10.5" width="14" height="9.5" rx="1" />
      <path d="M8 10.5V8a4 4 0 0 1 8 0v2.5" />
    </>
  ),
  globe: (
    <>
      <circle cx="12" cy="12" r="8.5" />
      <path d="M3.5 12h17M12 3.5c2.4 2.4 3.5 5.2 3.5 8.5s-1.1 6.1-3.5 8.5c-2.4-2.4-3.5-5.2-3.5-8.5s1.1-6.1 3.5-8.5z" />
    </>
  ),
  copy: (
    <>
      <rect x="8.5" y="8.5" width="11" height="11" rx="1" />
      <path d="M15.5 8.5V5.5a1 1 0 0 0-1-1h-9a1 1 0 0 0-1 1v9a1 1 0 0 0 1 1h3" />
    </>
  ),
  stream: (
    <>
      <path d="M4 6.5h16M4 11h10M4 15.5h13M4 20h7" />
    </>
  ),
  pause: <path d="M8.5 5.5v13M15.5 5.5v13" />,
  play: <path d="M7.5 5.5v13l11-6.5z" />,
  alert: (
    <>
      <path d="M12 4 2.8 19.5h18.4z" />
      <path d="M12 10v4.5M12 17h.01" />
    </>
  ),
  check: <path d="m5 12.5 4.5 4.5L19 7.5" />,
  layers: (
    <>
      <rect x="4" y="4" width="7" height="7" rx="0.5" />
      <rect x="13" y="4" width="7" height="7" rx="0.5" />
      <rect x="4" y="13" width="7" height="7" rx="0.5" />
      <rect x="13" y="13" width="7" height="7" rx="0.5" />
    </>
  ),
  user: (
    <>
      <circle cx="12" cy="8.5" r="3.5" />
      <path d="M5 20c1.2-3.6 3.8-5.5 7-5.5s5.8 1.9 7 5.5" />
    </>
  ),
};

export type IconName = keyof typeof PATHS;

/** Map a composition route or console leaf id to its icon. */
export function iconForLeaf(id: string): string {
  switch (id) {
    case "agent-runs":
      return "runs";
    case "chat":
      return "chat";
    case "controls":
      return "controls";
    case "library":
      return "library";
    case "profiles":
      return "profiles";
    case "harness-profiles":
      return "harness";
    case "skill-sets":
      return "skills";
    case "tool-sets":
      return "tools";
    case "councils":
      return "councils";
    case "volume-profiles":
      return "volumes";
    case "data-volumes":
      return "disk";
    case "auth-sessions":
      return "key";
    case "external-triggers":
      return "trigger";
    default:
      return "layers";
  }
}

export function Icon({ name, className }: { name: string; className?: string }) {
  return (
    <svg
      className={className ? `icon ${className}` : "icon"}
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth={1.7}
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
      focusable="false"
    >
      {PATHS[name] ?? PATHS.layers}
    </svg>
  );
}

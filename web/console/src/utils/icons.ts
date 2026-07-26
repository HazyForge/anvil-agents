/** Console UI presentation annotations on composition objects. */
export const ICON_ANNOTATION = "ui.anvil.hazyforge.io/icon";
export const SCREENSHOT_ANNOTATION = "ui.anvil.hazyforge.io/screenshot";

export interface BuiltInAvatar {
  id: string;
  label: string;
  /** Public path served by the console SPA. */
  src: string;
}

/** Built-in robot avatar pack shipped with the console. */
export const BUILTIN_AVATARS: BuiltInAvatar[] = [
  { id: "robot-01", label: "Herald", src: "/avatars/robot-01.jpg" },
  { id: "robot-02", label: "Archivist", src: "/avatars/robot-02.jpg" },
  { id: "robot-03", label: "Probe", src: "/avatars/robot-03.jpg" },
  { id: "robot-04", label: "Courier", src: "/avatars/robot-04.jpg" },
  { id: "robot-05", label: "Sentinel", src: "/avatars/robot-05.jpg" },
  { id: "robot-06", label: "Guardian", src: "/avatars/robot-06.jpg" },
  { id: "robot-07", label: "Ghost", src: "/avatars/robot-07.jpg" },
  { id: "robot-08", label: "Scout", src: "/avatars/robot-08.jpg" },
  { id: "robot-09", label: "Researcher", src: "/avatars/robot-09.jpg" },
  { id: "robot-10", label: "Engineer", src: "/avatars/robot-10.jpg" },
  { id: "robot-11", label: "Companion", src: "/avatars/robot-11.jpg" },
  { id: "robot-12", label: "Forge", src: "/avatars/robot-12.jpg" },
];

const builtinBySrc = new Map(BUILTIN_AVATARS.map((a) => [a.src, a]));
const builtinById = new Map(BUILTIN_AVATARS.map((a) => [a.id, a]));

export function annotationValue(
  annotations: Record<string, string> | undefined,
  key: string,
): string {
  if (!annotations) {
    return "";
  }
  return String(annotations[key] ?? "").trim();
}

export function getIconUrl(annotations: Record<string, string> | undefined): string {
  return annotationValue(annotations, ICON_ANNOTATION);
}

export function getScreenshotUrl(annotations: Record<string, string> | undefined): string {
  return annotationValue(annotations, SCREENSHOT_ANNOTATION);
}

/** Resolve a stored icon value (builtin id, path, or absolute URL) to a usable src. */
export function resolveIconSrc(value: string | undefined | null): string | undefined {
  const raw = String(value ?? "").trim();
  if (!raw) {
    return undefined;
  }
  if (builtinById.has(raw)) {
    return builtinById.get(raw)!.src;
  }
  if (builtinBySrc.has(raw)) {
    return raw;
  }
  // Allow relative console paths and absolute http(s)/data URLs.
  if (
    raw.startsWith("/") ||
    raw.startsWith("http://") ||
    raw.startsWith("https://") ||
    raw.startsWith("data:image/")
  ) {
    return raw;
  }
  // Bare avatar filename
  if (/^robot-\d{2}\.jpe?g$/i.test(raw)) {
    return `/avatars/${raw}`;
  }
  return raw;
}

export function mergePresentationAnnotations(
  existing: Record<string, string> | undefined,
  icon: string,
  screenshot: string,
): Record<string, string> | undefined {
  const next: Record<string, string> = { ...(existing ?? {}) };
  const iconTrim = icon.trim();
  const shotTrim = screenshot.trim();
  if (iconTrim) {
    next[ICON_ANNOTATION] = iconTrim;
  } else {
    delete next[ICON_ANNOTATION];
  }
  if (shotTrim) {
    next[SCREENSHOT_ANNOTATION] = shotTrim;
  } else {
    delete next[SCREENSHOT_ANNOTATION];
  }
  return Object.keys(next).length > 0 ? next : undefined;
}

export function initialFromName(name: string): string {
  const clean = name.trim();
  if (!clean) {
    return "?";
  }
  const parts = clean.split(/[-_\s.]+/).filter(Boolean);
  if (parts.length >= 2) {
    return (parts[0][0] + parts[1][0]).toUpperCase();
  }
  return clean.slice(0, 2).toUpperCase();
}

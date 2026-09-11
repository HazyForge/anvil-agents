/** Console UI presentation annotations on composition objects. */
import { AVATAR_FACES, avatarScene, resolveFaceId } from "../avatars/pack";

export const ICON_ANNOTATION = "ui.anvil.hazyforge.io/icon";
export const SCREENSHOT_ANNOTATION = "ui.anvil.hazyforge.io/screenshot";

export interface BuiltInAvatar {
  id: string;
  label: string;
  /** Public path of the static SVG (still image). Prefer storing `id`. */
  src: string;
}

/** Built-in Anvil Agents face pack. Source of truth: assets/agent-avatars. */
export const BUILTIN_AVATARS: BuiltInAvatar[] = AVATAR_FACES;

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

export type ResolvedIcon =
  | { kind: "face"; id: string; src: string }
  | { kind: "url"; src: string };

/** Resolve a stored icon value (face id, legacy JPEG path, or URL) to a usable icon. */
export function resolveIcon(value: string | undefined | null): ResolvedIcon | undefined {
  const raw = String(value ?? "").trim();
  if (!raw) {
    return undefined;
  }
  const faceId = resolveFaceId(avatarScene, raw);
  if (faceId) {
    return { kind: "face", id: faceId, src: `/avatars/${faceId}.svg` };
  }
  if (
    raw.startsWith("/") ||
    raw.startsWith("http://") ||
    raw.startsWith("https://") ||
    raw.startsWith("data:image/")
  ) {
    return { kind: "url", src: raw };
  }
  return { kind: "url", src: raw };
}

/** Resolve a stored icon value to a usable src (static SVG or URL). */
export function resolveIconSrc(value: string | undefined | null): string | undefined {
  return resolveIcon(value)?.src;
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
    const face = resolveIcon(iconTrim);
    next[ICON_ANNOTATION] = face?.kind === "face" ? face.id : iconTrim;
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

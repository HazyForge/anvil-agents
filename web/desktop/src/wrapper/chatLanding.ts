import type { CompositionDocument } from "../api/client";

export const DESKTOP_STANDING_ASSISTANT = "desktop-standing-assistant";

const standingLabel = "control.anvil.hazyforge.io/standing";

/** Prefer the Primaris standing Substrate assistant over a project-manager Job. */
export function preferredStandingChatProfile(
  profiles: CompositionDocument[],
): CompositionDocument | undefined {
  return (
    profiles.find((profile) => profile.metadata.name === DESKTOP_STANDING_ASSISTANT) ??
    profiles.find((profile) => profile.metadata.labels?.[standingLabel] === "true")
  );
}

/** Restore a saved thread only when it already sits on the standing assistant. */
export function shouldRestoreStandingThread(
  thread: { profileName?: string } | undefined,
  standing: CompositionDocument | undefined,
): boolean {
  const name = thread?.profileName?.trim();
  return Boolean(name && standing && name === standing.metadata.name);
}

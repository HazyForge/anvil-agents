import { type AgentRunView } from "../api/client";
import { isRunningPhase } from "./collaboration";

export const DESKTOP_REQPEER_PREFIX = "desktop-reqpeer-";
export const DESKTOP_PEER_PREFIX = "desktop-peer-";

export function isDesktopPairRunName(name: string): boolean {
  const value = (name || "").trim();
  return value.startsWith(DESKTOP_REQPEER_PREFIX) || value.startsWith(DESKTOP_PEER_PREFIX);
}

export function runningDesktopPairLock(runs: AgentRunView[]): AgentRunView[] {
  return runs.filter((run) => isRunningPhase(run.phase) && isDesktopPairRunName(run.name));
}

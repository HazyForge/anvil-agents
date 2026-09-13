import {
  AUDITOR_GROK_PROFILE,
  GROK_BACKEND,
  createAgentRun,
  getAgentRun,
  isGrokBackend,
  listAgentRuns,
  resolveGrokPeerCreate,
  type AgentRunView,
} from "../api/client";
import { grokInterruptDuplicatePrompt } from "./requestPeer";
import { isRunningPhase } from "./collaboration";
import { dnsLabel } from "./intent";

export const DESKTOP_REQPEER_PREFIX = "desktop-reqpeer-";
export const DESKTOP_PEER_PREFIX = "desktop-peer-";

export type CoordinatorCommand =
  | { kind: "status" }
  | { kind: "start"; profileName: string; prompt: string }
  | { kind: "requestPeer"; sourceRun: string }
  | { kind: "interrupt" }
  | { kind: "unknown"; raw: string };

export type CoordinatorResult = {
  text: string;
  runName?: string;
};

const HELP =
  "This Chat talks to the Desktop coordinator on Primaris, not a local CLI. Try: status · start <agent> <work> · request peer for <run>.";

export function parseCoordinatorCommand(text: string): CoordinatorCommand {
  const trimmed = text.trim();
  if (!trimmed) {
    return { kind: "unknown", raw: "" };
  }
  if (/^(status|report|what'?s running|running)\b/i.test(trimmed)) {
    return { kind: "status" };
  }
  const start = trimmed.match(/^start\s+(\S+)\s+(.+)$/i);
  if (start) {
    const profileName = dnsLabel(start[1]);
    const prompt = start[2].trim();
    if (profileName && prompt) {
      return { kind: "start", profileName, prompt };
    }
  }
  const peer = trimmed.match(/^request\s+peer(?:\s+for)?\s+(\S+)$/i);
  if (peer) {
    const sourceRun = dnsLabel(peer[1]) || peer[1].trim().toLowerCase();
    if (sourceRun) {
      return { kind: "requestPeer", sourceRun };
    }
  }
  if (/^interrupt\b/i.test(trimmed)) {
    return { kind: "interrupt" };
  }
  return { kind: "unknown", raw: trimmed };
}

export function isDesktopPairRunName(name: string): boolean {
  const value = (name || "").trim();
  return value.startsWith(DESKTOP_REQPEER_PREFIX) || value.startsWith(DESKTOP_PEER_PREFIX);
}

export function runningDesktopPairLock(runs: AgentRunView[]): AgentRunView[] {
  return runs.filter((run) => isRunningPhase(run.phase) && isDesktopPairRunName(run.name));
}

function formatLock(lock: AgentRunView[]): string {
  const names = lock.map((run) => `${run.name} (${run.phase || "Running"})`).join(", ");
  return `Not starting a run: ${names} still Running (shared grok-home).`;
}

function formatRuns(runs: AgentRunView[]): string {
  const running = runs.filter((run) => isRunningPhase(run.phase));
  if (running.length === 0) {
    const sample = runs.slice(0, 8).map((run) => `${run.name} ${run.phase || "—"}`).join("\n");
    return sample ? `Nothing Running.\n${sample}` : "No AgentRuns in this workspace.";
  }
  return running.map((run) => `${run.name}  ${run.phase || "—"}  ${run.backend || "—"}`).join("\n");
}

export async function runCoordinatorCommand(opts: {
  token: string;
  namespace: string;
  createEnabled: boolean;
  text: string;
}): Promise<CoordinatorResult> {
  const command = parseCoordinatorCommand(opts.text);

  if (command.kind === "unknown") {
    return { text: HELP };
  }

  if (command.kind === "interrupt") {
    return {
      text: "Live OIDC is append-only create. Desktop cannot fail or PATCH a run. Say which run if you need status.",
    };
  }

  if (command.kind === "status") {
    const runs = await listAgentRuns(opts.token, opts.namespace, 50);
    return { text: formatRuns(runs) };
  }

  const runs = await listAgentRuns(opts.token, opts.namespace, 50);
  const lock = runningDesktopPairLock(runs);
  if (lock.length > 0) {
    return { text: formatLock(lock) };
  }

  if (!opts.createEnabled) {
    return { text: "Starting a run is turned off on this server." };
  }

  if (command.kind === "start") {
    if (command.profileName === AUDITOR_GROK_PROFILE) {
      return {
        text: "The coordinator does not send Chat as a Production Auditor grok run. Name a specialist agent, or send status.",
      };
    }
    const created = await createAgentRun(opts.token, opts.namespace, {
      generateName: "desktop-chat-",
      prompt: command.prompt,
      profileName: command.profileName,
      backend: GROK_BACKEND,
    });
    const name = created.name?.trim() || "";
    return {
      text: name ? `Started specialist run ${name} (${command.profileName}).` : "Started a specialist run.",
      runName: name || undefined,
    };
  }

  const source = await getAgentRun(opts.token, opts.namespace, command.sourceRun);
  if (!isRunningPhase(source.phase)) {
    return { text: `Not posting a peer: ${command.sourceRun} is ${source.phase || "not Running"}.` };
  }
  const peers = runs.filter((run) => (run.name || "").startsWith(DESKTOP_PEER_PREFIX));
  const runningGrokPeer = peers.find(
    (run) => (isRunningPhase(run.phase) || (run.phase || "").trim() === "Pending") && isGrokBackend(run.backend),
  );
  if (runningGrokPeer) {
    return { text: `Skip peer POST: ${runningGrokPeer.name} already ${runningGrokPeer.phase || "Running"}.` };
  }
  const existingForSource = peers.find((run) => (run.source?.name || "").trim() === command.sourceRun);
  if (existingForSource) {
    return { text: `Skip peer POST: ${existingForSource.name} already exists for ${command.sourceRun}.` };
  }
  const resolved = await resolveGrokPeerCreate(opts.token, opts.namespace, {
    sourceRun: command.sourceRun,
    requestedProfileName: "",
    profiles: [],
  });
  const peerPrompt = grokInterruptDuplicatePrompt(command.sourceRun);
  const created = await createAgentRun(opts.token, opts.namespace, {
    generateName: DESKTOP_PEER_PREFIX,
    profileName: resolved.profileName,
    harnessProfileName: resolved.harnessProfileName,
    application: resolved.application,
    applicationName: resolved.application,
    backend: resolved.backend,
    prompt: peerPrompt,
    peerPrompt,
    sourceKind: "AgentRun",
    sourceName: command.sourceRun,
    sourceNamespace: opts.namespace,
  });
  const name = created.name?.trim() || "";
  return {
    text: name ? `Posted grok peer ${name} for ${command.sourceRun}.` : "Posted a grok peer.",
    runName: name || undefined,
  };
}

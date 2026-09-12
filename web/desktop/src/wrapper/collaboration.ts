import { createAgentRun, getAgentRun } from "../api/client";

export const PEER_SIGNAL = /\b(confer|conferral|peer consult|interrupt|collaborat|duplicate work|ask.*peer|peer run)\b/i;

export function isRunningPhase(phase?: string): boolean {
  return (phase || "").trim() === "Running";
}

export function peerSignalText(text: string): boolean {
  return PEER_SIGNAL.test(text);
}

function workerPrompt(worker: "A" | "B", objective: string, peerRunName: string | null): string {
  const peerClause = peerRunName
    ? `The peer AgentRun already in flight is ${peerRunName}. Before duplicating build or research work, confer with or interrupt that peer run using the cluster agent tooling available to you (for example anvil-agentctl / status helpers). Log what you decided.`
    : "A second peer AgentRun on the same objective will start after you are Running. Prepare to confer once it appears; avoid duplicate build work.";
  return [
    `Shared desktop objective: ${objective}`,
    `You are grok worker ${worker} on Anvil Primaris.`,
    peerClause,
    "Stay on the shared objective. Prefer efficiency over parallel duplicate builds.",
  ].join("\n\n");
}

export async function waitForRunningPhase(
  token: string,
  namespace: string,
  runName: string,
  timeoutMs: number,
  onTick?: (phase: string) => void,
): Promise<void> {
  const deadline = Date.now() + timeoutMs;
  while (Date.now() < deadline) {
    const run = await getAgentRun(token, namespace, runName);
    const phase = run.phase || "";
    onTick?.(phase);
    if (isRunningPhase(phase)) {
      return;
    }
    if (phase === "Succeeded" || phase === "Failed" || phase === "NeedsHuman") {
      throw new Error(`${runName} reached ${phase} before peer overlap window`);
    }
    await sleep(2500);
  }
  throw new Error(`timed out waiting for ${runName} to reach Running`);
}

export async function startStaggeredPairedObjective(opts: {
  token: string;
  namespace: string;
  profileName: string;
  objective: string;
  onStatus: (message: string) => void;
}): Promise<{ runA: string; runB: string }> {
  const objective = opts.objective.trim();
  const profileName = opts.profileName.trim();
  if (!objective) {
    throw new Error("objective is required");
  }
  if (!profileName) {
    throw new Error("grok AgentRunProfile name is required");
  }

  opts.onStatus("Creating worker A…");
  const first = await createAgentRun(opts.token, opts.namespace, {
    generateName: "desktop-collab-a-",
    prompt: workerPrompt("A", objective, null),
    profileName,
  });
  const runA = runNameFromCreate(first);
  if (!runA) {
    throw new Error("API did not return worker A run name");
  }

  opts.onStatus(`Waiting for ${runA} → Running before starting worker B…`);
  await waitForRunningPhase(opts.token, opts.namespace, runA, 240_000, (phase) => {
    opts.onStatus(`${runA} phase: ${phase || "—"}`);
  });

  opts.onStatus(`Worker A is Running. Creating worker B to overlap on the same objective…`);
  const second = await createAgentRun(opts.token, opts.namespace, {
    generateName: "desktop-collab-b-",
    prompt: workerPrompt("B", objective, runA),
    profileName,
  });
  const runB = runNameFromCreate(second);
  if (!runB) {
    throw new Error("API did not return worker B run name");
  }

  opts.onStatus(`Paired objective started: ${runA} + ${runB}`);
  return { runA, runB };
}

function runNameFromCreate(payload: unknown): string | null {
  if (!payload || typeof payload !== "object") {
    return null;
  }
  const name = (payload as { name?: string }).name;
  return typeof name === "string" && name.trim() ? name.trim() : null;
}

function sleep(ms: number): Promise<void> {
  return new Promise((resolve) => window.setTimeout(resolve, ms));
}

export const STATUS_JSON_PREFIX = "ANVIL_AGENT_RUN_STATUS_JSON=";

export type RequestPeerPayload = {
  peerProfileName: string;
  summary?: string;
  stage?: string;
};

export type InterruptDuplicatePayload = {
  duplicateRunName?: string;
  summary?: string;
};

function statusJSONFromLogLine(line: string): Record<string, unknown> | null {
  const index = line.indexOf(STATUS_JSON_PREFIX);
  if (index < 0) {
    return null;
  }
  const raw = line.slice(index + STATUS_JSON_PREFIX.length).trim();
  if (!raw) {
    return null;
  }
  try {
    const body = JSON.parse(raw) as Record<string, unknown>;
    return body && typeof body === "object" ? body : null;
  } catch {
    return null;
  }
}

function decisionAction(body: Record<string, unknown>): string {
  const type = typeof body.type === "string" ? body.type.trim() : "";
  const action = typeof body.action === "string" ? body.action.trim() : "";
  if (type === "decision") {
    return action;
  }
  return type;
}

export function parseRequestPeerFromLogLine(line: string): RequestPeerPayload | null {
  const body = statusJSONFromLogLine(line);
  if (!body) {
    return null;
  }
  if (decisionAction(body) !== "requestPeer") {
    return null;
  }
  const peerProfileName =
    typeof body.peerProfileName === "string"
      ? body.peerProfileName.trim()
      : typeof body.peerProfile === "string"
        ? body.peerProfile.trim()
        : "";
  return {
    peerProfileName,
    summary: typeof body.summary === "string" ? body.summary : undefined,
    stage: typeof body.stage === "string" ? body.stage : undefined,
  };
}

export function parseInterruptDuplicateFromLogLine(line: string): InterruptDuplicatePayload | null {
  const body = statusJSONFromLogLine(line);
  if (!body) {
    return null;
  }
  if (decisionAction(body) !== "interruptDuplicate") {
    return null;
  }
  return {
    duplicateRunName:
      typeof body.duplicateRunName === "string" ? body.duplicateRunName.trim() : undefined,
    summary: typeof body.summary === "string" ? body.summary : undefined,
  };
}

export function isInterruptDuplicateLogLine(line: string): boolean {
  return parseInterruptDuplicateFromLogLine(line) !== null;
}

export function grokRequestPeerProofPrompt(peerProfileName?: string): string {
  const body: Record<string, unknown> = {
    type: "decision",
    action: "requestPeer",
    summary: "Need a grok peer with a different grok-home before duplicate work on this objective.",
  };
  if (peerProfileName?.trim()) {
    body.peerProfileName = peerProfileName.trim();
  }
  const json = JSON.stringify(body);
  return [
    "Primaris Desktop grok proof (single run).",
    "While you are still Running, emit exactly one stdout line in this harness format:",
    `${STATUS_JSON_PREFIX}${json}`,
    "Request a grok AgentRunProfile that does not share this run's grok-home data volume.",
    "Do not request this same profile, desktop-grok-proof-conferral-b, or hazy-trade-human-comms-smoke.",
    "You may use anvil-agentctl or anvil-agent-status if available, but the line above must appear verbatim in job logs.",
    "After emitting that requestPeer line, stay Running for about 10 minutes so Desktop can POST sibling B and that peer can overlap while you are still Running. Sleep or otherwise hold (for example sleep 600). Do not exit, do not 'finish cleanly', and do not wrap up the job right after the line.",
    "Do not invent a peer run yourself — Desktop will POST a grok sibling via OIDC when the line appears.",
  ].join("\n\n");
}

export function grokInterruptDuplicatePrompt(duplicateRunName: string): string {
  const json = JSON.stringify({
    type: "decision",
    action: "interruptDuplicate",
    duplicateRunName,
    summary: "Another grok run is already on this objective; interrupt duplicate work.",
  });
  return [
    "Primaris Desktop grok sibling (peer B).",
    `A grok AgentRun named ${duplicateRunName} is already Running on the same objective.`,
    "Your first stdout work must be this exact line, emitted immediately while that duplicate run is still Running. Do not research, clone, or start the objective before this line appears in job logs:",
    `${STATUS_JSON_PREFIX}${json}`,
    "You may use anvil-agentctl or anvil-agent-status if available, but the line above must appear verbatim in job logs as the first real action.",
    "Do not wander into a long clone of the objective before emitting interruptDuplicate. After the line is in logs you may idle briefly; the overlap with the duplicate run is the proof.",
  ].join("\n\n");
}

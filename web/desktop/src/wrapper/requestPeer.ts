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
    "While you are still executing (before finishing), emit exactly one stdout line in this harness format:",
    `${STATUS_JSON_PREFIX}${json}`,
    "Request a grok AgentRunProfile that does not share this run's grok-home data volume.",
    "Do not request this same profile, desktop-grok-proof-conferral-b, or hazy-trade-human-comms-smoke.",
    "You may use anvil-agentctl or anvil-agent-status if available, but the line above must appear verbatim in job logs.",
    "After emitting requestPeer, continue briefly then exit cleanly.",
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
    "While you are still executing AND while that peer is still Running, emit exactly one stdout line:",
    `${STATUS_JSON_PREFIX}${json}`,
    "You may use anvil-agentctl or anvil-agent-status if available, but the line above must appear verbatim in job logs.",
    "After emitting interruptDuplicate, continue briefly then exit cleanly.",
    "Do not start a new clone of the work already in flight on the duplicate run.",
  ].join("\n\n");
}

export const STATUS_JSON_PREFIX = "ANVIL_AGENT_RUN_STATUS_JSON=";

export type RequestPeerPayload = {
  peerProfileName: string;
  summary?: string;
  stage?: string;
};

export function parseRequestPeerFromLogLine(line: string): RequestPeerPayload | null {
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
    const type = typeof body.type === "string" ? body.type.trim() : "";
    if (type !== "requestPeer") {
      return null;
    }
    const peerProfileName =
      typeof body.peerProfileName === "string"
        ? body.peerProfileName.trim()
        : typeof body.peerProfile === "string"
          ? body.peerProfile.trim()
          : "";
    if (!peerProfileName) {
      return null;
    }
    return {
      peerProfileName,
      summary: typeof body.summary === "string" ? body.summary : undefined,
      stage: typeof body.stage === "string" ? body.stage : undefined,
    };
  } catch {
    return null;
  }
}

export function grokRequestPeerProofPrompt(peerProfileName: string): string {
  const json = JSON.stringify({
    type: "requestPeer",
    peerProfileName,
    stage: "peer-request",
    summary: "Desktop proof: request human-comms peer before duplicate work",
  });
  return [
    "Primaris Desktop grok proof (single run).",
    "While you are still executing (before finishing), emit exactly one stdout line in this harness format:",
    `${STATUS_JSON_PREFIX}${json}`,
    "You may use anvil-agentctl or anvil-agent-status if available, but the line above must appear verbatim in job logs.",
    "After emitting requestPeer, continue briefly then exit cleanly.",
    "Do not invent a peer run yourself — Desktop will POST it via OIDC when the line appears.",
  ].join("\n\n");
}

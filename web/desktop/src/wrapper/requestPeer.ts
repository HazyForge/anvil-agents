export const STATUS_JSON_PREFIX = "ANVIL_AGENT_RUN_STATUS_JSON=";

export type RequestPeerPayload = {
  peerProfileName: string;
  summary?: string;
  stage?: string;
  /** requestPeer extension: peers ask Wrapper/manager to create-agent. */
  request?: string;
  action?: string;
  name?: string;
  agentName?: string;
  description?: string;
  title?: string;
  systemPrompt?: string;
  harnessProfileName?: string;
};

export type InterruptDuplicatePayload = {
  duplicateRunName?: string;
  summary?: string;
};

function extractJSONObject(text: string, from: number): { body: Record<string, unknown>; end: number } | null {
  const start = text.indexOf("{", from);
  if (start < 0) {
    return null;
  }
  let depth = 0;
  let end = -1;
  for (let i = start; i < text.length; i++) {
    const ch = text[i];
    if (ch === "{") {
      depth += 1;
    } else if (ch === "}") {
      depth -= 1;
      if (depth === 0) {
        end = i;
        break;
      }
    }
  }
  if (end < 0) {
    return null;
  }
  try {
    const body = JSON.parse(text.slice(start, end + 1)) as Record<string, unknown>;
    if (!body || typeof body !== "object") {
      return null;
    }
    return { body, end };
  } catch {
    return null;
  }
}

function statusJSONFromLogLine(line: string): Record<string, unknown> | null {
  const index = line.indexOf(STATUS_JSON_PREFIX);
  if (index < 0) {
    return null;
  }
  return extractJSONObject(line, index + STATUS_JSON_PREFIX.length)?.body ?? null;
}

export function extractStatusBodies(text: string): Record<string, unknown>[] {
  const out: Record<string, unknown>[] = [];
  if (!text) {
    return out;
  }
  let from = 0;
  while (from < text.length) {
    const index = text.indexOf(STATUS_JSON_PREFIX, from);
    if (index < 0) {
      break;
    }
    const parsed = extractJSONObject(text, index + STATUS_JSON_PREFIX.length);
    if (!parsed) {
      from = index + STATUS_JSON_PREFIX.length;
      continue;
    }
    out.push(parsed.body);
    from = parsed.end + 1;
  }
  return out;
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
  const action = decisionAction(body);
  const request = typeof body.request === "string" ? body.request.trim() : "";
  const isCreateRequest =
    action === "requestCreateAgent" ||
    request === "create-agent" ||
    request === "create_agent" ||
    request === "requestCreateAgent";
  if (action !== "requestPeer" && !isCreateRequest) {
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
    request: request || (isCreateRequest ? "create-agent" : undefined),
    action: action || undefined,
    name: typeof body.name === "string" ? body.name.trim() : undefined,
    agentName: typeof body.agentName === "string" ? body.agentName.trim() : undefined,
    description: typeof body.description === "string" ? body.description.trim() : undefined,
    title: typeof body.title === "string" ? body.title.trim() : undefined,
    systemPrompt: typeof body.systemPrompt === "string" ? body.systemPrompt.trim() : undefined,
    harnessProfileName: typeof body.harnessProfileName === "string" ? body.harnessProfileName.trim() : undefined,
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
  return conferralActionsFromText(line).includes("interruptDuplicate");
}

export function isRequestPeerLogLine(line: string): boolean {
  return conferralActionsFromText(line).includes("requestPeer");
}

export type ConferralAction = "requestPeer" | "interruptDuplicate";

const REQUEST_PEER_EMITTED =
  /\bEmitted requestPeer\b|\brequestPeer line emitted\b|"action"\s*:\s*"requestPeer"|action=requestPeer/;
const INTERRUPT_DUPLICATE_ACTION =
  /"action"\s*:\s*"interruptDuplicate"|action=interruptDuplicate/;

export function conferralActionsFromText(text: string): ConferralAction[] {
  const seen = new Set<ConferralAction>();
  if (!text) {
    return [];
  }
  for (const body of extractStatusBodies(text)) {
    const action = decisionAction(body);
    if (action === "requestPeer" || action === "interruptDuplicate") {
      seen.add(action);
    }
    const blob = [body.summary, body.detail].filter(Boolean).join(" ");
    if (REQUEST_PEER_EMITTED.test(blob)) {
      seen.add("requestPeer");
    }
  }
  if (REQUEST_PEER_EMITTED.test(text)) {
    seen.add("requestPeer");
  }
  if (INTERRUPT_DUPLICATE_ACTION.test(text)) {
    seen.add("interruptDuplicate");
  }
  return [...seen];
}

export function stickyConferralAction(run: {
  output?: string;
  decision?: { action?: string; summary?: string };
  reports?: { type?: string; summary?: string; detail?: string }[];
}): string {
  const blobs = [
    run.output || "",
    run.decision?.summary || "",
    ...(run.reports ?? []).map((report) => [report.type, report.summary, report.detail].filter(Boolean).join(" ")),
  ];
  const fromOutput = conferralActionsFromText(blobs.join("\n"));
  if (fromOutput.includes("interruptDuplicate")) {
    return "interruptDuplicate";
  }
  if (fromOutput.includes("requestPeer")) {
    return "requestPeer";
  }
  const cr = (run.decision?.action || "").trim();
  if (cr === "requestPeer" || cr === "interruptDuplicate") {
    return cr;
  }
  return cr;
}

export function preferStickyConferral(previous: string, next: string): string {
  const sticky = (value: string) => value === "requestPeer" || value === "interruptDuplicate";
  if (sticky(previous) && (next === "observe" || next === "completed" || !next)) {
    return previous;
  }
  if (sticky(next)) {
    return next;
  }
  return next || previous;
}

export function peerRunBelongsToSource(
  run: { source?: { name?: string; namespace?: string }; name?: string },
  sourceRun: string,
  sourceNamespace?: string,
): boolean {
  if ((run.source?.name || "").trim() !== sourceRun.trim()) {
    return false;
  }
  const sourceNs = (sourceNamespace || "").trim();
  const runNs = (run.source?.namespace || "").trim();
  if (sourceNs && runNs && sourceNs !== runNs) {
    return false;
  }
  return true;
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
    "After emitting that requestPeer line, stay Running for about 15 minutes so Desktop can POST sibling B and that peer can overlap while you are still Running. Sleep or otherwise hold (for example sleep 900). Do not exit, do not 'finish cleanly', and do not wrap up the job right after the line.",
    "Do not invent a peer run yourself — Desktop will POST a grok sibling via OIDC when the line appears.",
  ].join("\n\n");
}

export function grokInterruptDuplicatePrompt(duplicateRunName: string): string {
  const name = duplicateRunName.trim();
  const interruptLine = `${STATUS_JSON_PREFIX}${JSON.stringify({
    type: "decision",
    action: "interruptDuplicate",
    duplicateRunName: name,
    summary: `${name} is already Running on this objective; interrupt duplicate work.`,
  })}`;
  const observeLine = `${STATUS_JSON_PREFIX}${JSON.stringify({
    type: "decision",
    action: "observe",
    summary: `${name} is not Running on this objective.`,
  })}`;
  return [
    "You are grok on this AgentRun. Authorized work is duplicate judgment only.",
    `DUPLICATE_RUN_NAME=${name}`,
    "Judge whether DUPLICATE_RUN_NAME is already Running on the same objective as this peer. Inspect that run's phase and objective before deciding.",
    `If yes: emit interruptDuplicate STATUS_JSON (duplicateRunName=${name}) then complete. Example after a yes:\n${interruptLine}`,
    `If not: emit a different decision (action observe is fine) and exit 0. Example:\n${observeLine}`,
    "Emit ANVIL_AGENT_RUN_STATUS_JSON= from this grok turn (stdout or anvil-agentctl self report / anvil-agent-status). Do not wrap printf around grok. Do not printf the line as the first action before judging.",
  ].join("\n\n");
}

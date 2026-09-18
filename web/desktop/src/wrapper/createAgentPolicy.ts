import type { ChatChip } from "../api/types.chat.ts";
import { dnsLabel, explicitAgentNames, parseWrapperIntent, WRAPPER_PROFILE_NAME } from "./intent.ts";
import {
  extractStatusBodies,
  parseRequestPeerFromLogLine,
  STATUS_JSON_PREFIX,
  type RequestPeerPayload,
} from "./requestPeer.ts";

function isRecord(value: unknown): value is Record<string, unknown> {
  return Boolean(value) && typeof value === "object" && !Array.isArray(value);
}

function extractNamedToolCalls(payload: unknown): { name: string; args: Record<string, unknown> }[] {
  if (!isRecord(payload)) {
    return [];
  }
  const out: { name: string; args: Record<string, unknown> }[] = [];
  const push = (name: unknown, args: unknown) => {
    if (typeof name !== "string" || !name.trim()) {
      return;
    }
    let parsed = args;
    if (typeof parsed === "string") {
      try {
        parsed = JSON.parse(parsed);
      } catch {
        parsed = {};
      }
    }
    out.push({ name: name.trim(), args: isRecord(parsed) ? parsed : {} });
  };
  push(payload.name || payload.tool, payload.arguments || payload.args || payload.parameters);
  const list = payload.toolCalls || payload.tool_calls;
  if (Array.isArray(list)) {
    for (const item of list) {
      if (!isRecord(item)) {
        continue;
      }
      const fn = isRecord(item.function) ? item.function : item;
      push(fn.name || item.name || item.tool, fn.arguments || fn.args || item.parameters);
    }
  }
  return out;
}

/** Baked-in tool/skill name. Wrapper and manager only. */
export const CREATE_AGENT_TOOL = "create-agent";

export const CREATE_AGENT_ROLE_LABEL = "control.anvil.hazyforge.io/role";
export const CREATE_AGENT_ALLOW_LABEL = "control.anvil.hazyforge.io/create-agent";

/** Always-on manager harness profiles. Dedicated grok-home — never auditor or desktop-grok-peer. */
export const MANAGER_PROFILE_NAMES = [
  "desktop-manager",
  "manager-hazy-trade",
  "anvil-primaris-agent-manager",
  "hazy-trade-agent-manager",
] as const;

export const CREATE_AGENT_SKILL_DESCRIPTION =
  "Create a named teammate (AgentRunProfile + standing-chat thread when chat is enabled). Wrapper and manager only; peers request via requestPeer.";

export const CREATE_AGENT_SKILL_CONTENT = `You have the baked-in create-agent tool. Only the Desktop Wrapper or a project manager may create AgentRunProfiles.

When the operator wants a new teammate, call create-agent with:
- name: DNS-1123 label (required)
- description or title: what the teammate does
- systemPrompt: optional standing prompt
- harnessProfileName: optional same-namespace harness override

Desktop fulfills the POST (composition write). You do not receive OIDC tokens and must not invent kube credentials.

After success, tell peers the new profile name so they can address it.

If you are not Wrapper/manager, do not call create-agent. Emit requestPeer STATUS_JSON instead:

${STATUS_JSON_PREFIX}{"type":"decision","action":"requestPeer","request":"create-agent","name":"<dns-label>","description":"<why>","peerProfileName":"desktop-manager"}

Peers who call create-agent themselves are refused.`;

export const CREATE_AGENT_PEER_REFUSAL =
  "create-agent is Wrapper/manager only. Request it from the manager or Wrapper with requestPeer (request=create-agent). This principal cannot create AgentRunProfiles.";

export type CreateAgentInput = {
  name: string;
  description?: string;
  title?: string;
  systemPrompt?: string;
  harnessProfileName?: string;
};

export type CreateAgentSuccess = {
  ok: true;
  created: boolean;
  profileName: string;
  profile?: { metadata: { name: string } };
  threadId?: string;
  principal: string;
  input: CreateAgentInput;
};

export type CreateAgentFailure = {
  ok: false;
  refused: boolean;
  reason: string;
  principal?: string;
  input?: CreateAgentInput;
};

export type CreateAgentResult = CreateAgentSuccess | CreateAgentFailure;

export type CreateAgentRequest = CreateAgentInput & {
  peerProfileName?: string;
  summary?: string;
};

export function isWrapperPrincipal(profileName: string | undefined): boolean {
  const name = (profileName || "").trim().toLowerCase();
  return name === WRAPPER_PROFILE_NAME || name === "wrapper";
}

export function isManagerPrincipal(
  profileName: string | undefined,
  labels?: Record<string, string>,
): boolean {
  const name = (profileName || "").trim().toLowerCase();
  if (!name) {
    return roleAllowsCreateAgent(labels);
  }
  for (const allowed of MANAGER_PROFILE_NAMES) {
    if (name === allowed) {
      return true;
    }
  }
  if (name.endsWith("-agent-manager")) {
    return true;
  }
  return roleAllowsCreateAgent(labels);
}

function roleAllowsCreateAgent(labels?: Record<string, string>): boolean {
  if (!labels) {
    return false;
  }
  const role = (labels[CREATE_AGENT_ROLE_LABEL] || "").trim().toLowerCase();
  if (role === "wrapper" || role === "manager") {
    return true;
  }
  return (labels[CREATE_AGENT_ALLOW_LABEL] || "").trim().toLowerCase() === "allow";
}

/** Authority check. chat-role=project-manager is presentation only and does not grant create. */
export function isCreateAgentPrincipal(
  profileName: string | undefined,
  labels?: Record<string, string>,
): boolean {
  if (isWrapperPrincipal(profileName)) {
    return true;
  }
  return isManagerPrincipal(profileName, labels);
}

export function normalizeCreateAgentInput(raw: unknown): CreateAgentInput | null {
  const record = isRecord(raw) ? raw : {};
  const name = dnsLabel(String(record.name || record.profileName || record.agent || "").trim());
  if (!name || name === WRAPPER_PROFILE_NAME) {
    return null;
  }
  const description = trimOptional(record.description) || trimOptional(record.title);
  const title = trimOptional(record.title);
  const systemPrompt = trimOptional(record.systemPrompt) || trimOptional(record.prompt);
  const harnessProfileName = dnsLabel(
    String(record.harnessProfileName || record.harnessProfileRef || "").trim(),
  );
  return {
    name,
    description: description || undefined,
    title: title || undefined,
    systemPrompt: systemPrompt || undefined,
    harnessProfileName: harnessProfileName || undefined,
  };
}

function trimOptional(value: unknown): string {
  return typeof value === "string" ? value.trim() : "";
}

export function parseCreateAgentToolArgs(args: Record<string, unknown> | undefined): CreateAgentInput | null {
  if (!args) {
    return null;
  }
  return normalizeCreateAgentInput(args);
}

export function isCreateAgentToolName(name: string | undefined): boolean {
  const value = (name || "").trim().toLowerCase();
  return value === CREATE_AGENT_TOOL || value === "create_agent" || value === "createagent";
}

export function isCreateAgentRequestName(value: string | undefined): boolean {
  const request = (value || "").trim().toLowerCase();
  return request === "requestcreateagent" || request === "request-create-agent";
}

export function peerIsCreateAgentRequest(peer: RequestPeerPayload | null): boolean {
  if (!peer) {
    return false;
  }
  if (isCreateAgentRequestName(peer.action) || isCreateAgentRequestName(peer.request)) {
    return true;
  }
  const action = (peer.action || "").trim();
  return (action === "requestPeer" || action === "") && isCreateAgentToolName(peer.request);
}

export function createAgentRequestFromPeer(peer: RequestPeerPayload | null): CreateAgentRequest | null {
  if (!peerIsCreateAgentRequest(peer) || !peer) {
    return null;
  }
  const input = normalizeCreateAgentInput({
    name: peer.name || peer.agentName,
    description: peer.description || peer.summary,
    title: peer.title,
    systemPrompt: peer.systemPrompt,
    harnessProfileName: peer.harnessProfileName,
  });
  if (!input) {
    return null;
  }
  return {
    ...input,
    peerProfileName: peer.peerProfileName || undefined,
    summary: peer.summary,
  };
}

export function parseCreateAgentRequestFromLogLine(line: string): CreateAgentRequest | null {
  return createAgentRequestFromPeer(parseRequestPeerFromLogLine(line));
}

export function parseCreateAgentToolCalls(payload: unknown): CreateAgentInput[] {
  const out: CreateAgentInput[] = [];
  for (const tc of extractNamedToolCalls(payload)) {
    if (!isCreateAgentToolName(tc.name)) {
      continue;
    }
    const input = parseCreateAgentToolArgs(tc.args);
    if (input) {
      out.push(input);
    }
  }
  return out;
}

export function parseCreateAgentFromStatusBodies(text: string): {
  creates: CreateAgentInput[];
  requests: CreateAgentRequest[];
} {
  const creates: CreateAgentInput[] = [];
  const requests: CreateAgentRequest[] = [];
  for (const body of extractStatusBodies(text)) {
    const action = String(body.action || "").trim();
    const request = String(body.request || "").trim();
    const peer = {
      peerProfileName: String(body.peerProfileName || "").trim(),
      summary: typeof body.summary === "string" ? body.summary : undefined,
      request,
      action: action || String(body.type || "").trim(),
      name: typeof body.name === "string" ? body.name : undefined,
      agentName: typeof body.agentName === "string" ? body.agentName : undefined,
      description: typeof body.description === "string" ? body.description : undefined,
      title: typeof body.title === "string" ? body.title : undefined,
      systemPrompt: typeof body.systemPrompt === "string" ? body.systemPrompt : undefined,
      harnessProfileName: typeof body.harnessProfileName === "string" ? body.harnessProfileName : undefined,
    };
    const req = createAgentRequestFromPeer(peer);
    if (req) {
      requests.push(req);
      continue;
    }
    if (isCreateAgentToolName(action)) {
      const input = normalizeCreateAgentInput(body);
      if (input) {
        creates.push(input);
      }
    }
  }
  return { creates, requests };
}

function descriptionFromNamedClause(text: string, name: string): string | undefined {
  const escaped = name.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
  const namedThat = text.match(new RegExp(`\\bnamed\\s+${escaped}\\s+that\\s+(.+?)(?:[.!?;]|$)`, "i"));
  if (namedThat?.[1]) {
    return namedThat[1].trim();
  }
  const calledThat = text.match(new RegExp(`\\bcalled\\s+${escaped}\\s+that\\s+(.+?)(?:[.!?;]|$)`, "i"));
  if (calledThat?.[1]) {
    return calledThat[1].trim();
  }
  const colon = text.match(new RegExp(`\\b${escaped}\\s*:\\s*(.+?)(?:[.!?;]|$)`, "i"));
  if (colon?.[1]) {
    return colon[1].trim();
  }
  return undefined;
}

/** Natural-language create-agent (Grok Bot-style name + description). */
export function parseCreateAgentIntent(
  text: string,
  existing: string[] = [],
  opts?: { generateNames?: boolean },
): CreateAgentInput[] {
  const trimmed = text.trim();
  if (!trimmed) {
    return [];
  }
  const { creates } = parseCreateAgentFromStatusBodies(trimmed);
  if (creates.length > 0) {
    return creates;
  }
  const toolCreates = parseCreateAgentToolCalls(tryParseJSON(trimmed));
  if (toolCreates.length > 0) {
    return toolCreates;
  }
  const intent = parseWrapperIntent(trimmed, existing);
  if (!intent.spawn) {
    return [];
  }
  const names = explicitAgentNames(trimmed);
  if (names.length > 0) {
    return names.map((name) => ({
      name,
      description: descriptionFromNamedClause(trimmed, name),
    }));
  }
  if (!opts?.generateNames || intent.names.length === 0) {
    return [];
  }
  return intent.names.map((name) => ({
    name,
    description: descriptionFromNamedClause(trimmed, name),
  }));
}

function tryParseJSON(text: string): unknown {
  const trimmed = text.trim();
  if (!trimmed.startsWith("{") && !trimmed.startsWith("[")) {
    return null;
  }
  try {
    return JSON.parse(trimmed);
  } catch {
    return null;
  }
}

export function createAgentChips(result: CreateAgentResult): ChatChip[] {
  if (result.ok) {
    return [
      { id: "tool-create-agent", type: "tool", label: CREATE_AGENT_TOOL },
      {
        id: `created-${result.profileName}`,
        type: "transition",
        label: result.created ? `Created: ${result.profileName}` : `Already existed: ${result.profileName}`,
        status: "completed",
      },
    ];
  }
  if (result.refused) {
    const name = result.input?.name;
    return [
      { id: "tool-create-agent", type: "tool", label: CREATE_AGENT_TOOL },
      {
        id: "create-agent-refused",
        type: "transition",
        label: name ? `Refused create: ${name}` : "Refused: create-agent (not wrapper/manager)",
        status: "failed",
      },
    ];
  }
  return [
    {
      id: "create-agent-error",
      type: "transition",
      label: result.reason,
      status: "failed",
    },
  ];
}

export function requestedCreateChips(request: CreateAgentRequest): ChatChip[] {
  return [
    { id: "routing-request-create", type: "routing", label: "Routing: requestPeer" },
    {
      id: `requested-create-${request.name}`,
      type: "transition",
      label: `Requested create: ${request.name}`,
      status: "pending",
    },
  ];
}

export function profilePrompt(name: string, description: string): string {
  const role = description.trim() || "Work with peer agents spawned in the same namespace.";
  return `You are ${name}, an Anvil agent spawned by Anvil Agents Desktop. ${role} You do not receive OIDC tokens. Talk to peers in short sentences. You cannot create AgentRunProfiles; request create-agent from Wrapper or manager via requestPeer.`;
}

export function formatCreateAgentReceipt(result: CreateAgentResult): string {
  if (result.ok) {
    const verb = result.created ? "Created" : "Already existed";
    const thread = result.threadId ? ` Standing chat thread ${result.threadId}.` : "";
    return `${verb} agent ${result.profileName}. Peers can address this profile.${thread}`;
  }
  return result.reason;
}

export { WRAPPER_PROFILE_NAME };

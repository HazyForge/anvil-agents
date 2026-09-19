import { isCreateAgentPrincipal } from "./createAgentPolicy.ts";
import { extractStatusBodies } from "./requestPeer.ts";

/** User-message metadata key set by the API turn path (see docs/jev-intent-routing.md). */
export const JEV_NEEDS_PEER_HANDOFF_METADATA_KEY = "jevNeedsPeerHandoff";

/** Turn AgentRun annotation mirroring the metadata flag for kubectl-visible discovery. */
export const JEV_NEEDS_PEER_HANDOFF_ANNOTATION =
  "control.anvil.hazyforge.io/jev-needs-peer-handoff";

export type JevMessageLike = {
  content?: string;
  metadata?: unknown;
};

export type PeerHandoffTarget = {
  peerProfileName: string;
  summary?: string;
};

function isRecord(value: unknown): value is Record<string, unknown> {
  return Boolean(value) && typeof value === "object" && !Array.isArray(value);
}

/**
 * True when thread detail marks this user message as a classified,
 * non-unclear peer_handoff needing coordination through existing paths.
 * Request flag only — never authorizes creation or new fanout.
 */
export function messageNeedsPeerHandoff(message: JevMessageLike | undefined | null): boolean {
  if (!message || !isRecord(message.metadata)) {
    return false;
  }
  return message.metadata[JEV_NEEDS_PEER_HANDOFF_METADATA_KEY] === true;
}

/**
 * True when a turn's AgentRun annotations carry the peer-handoff request
 * flag. Same request-only semantics as the message metadata flag.
 */
export function runNeedsPeerHandoff(
  annotations: Record<string, string> | undefined | null,
): boolean {
  if (!annotations) {
    return false;
  }
  return annotations[JEV_NEEDS_PEER_HANDOFF_ANNOTATION] === "true";
}

/** Latest message in the thread carrying the peer-handoff request flag. */
export function latestPeerHandoffMessage<T extends JevMessageLike>(
  messages: T[] | null | undefined,
): T | undefined {
  if (!Array.isArray(messages)) {
    return undefined;
  }
  for (let i = messages.length - 1; i >= 0; i--) {
    if (messageNeedsPeerHandoff(messages[i])) {
      return messages[i];
    }
  }
  return undefined;
}

function isCreateAgentRequestBody(body: Record<string, unknown>): boolean {
  const pick = (value: unknown) => (typeof value === "string" ? value.trim().toLowerCase() : "");
  const request = pick(body.request);
  const action = pick(body.action);
  return (
    action === "requestcreateagent" ||
    request === "requestcreateagent" ||
    request === "request-create-agent" ||
    request === "create-agent" ||
    request === "create_agent" ||
    request === "createagent"
  );
}

function decisionAction(body: Record<string, unknown>): string {
  const type = typeof body.type === "string" ? body.type.trim() : "";
  const action = typeof body.action === "string" ? body.action.trim() : "";
  if (type === "decision") {
    return action;
  }
  return type;
}

/**
 * Existing-peer handoff targets named by requestPeer STATUS_JSON in free
 * text. Same shape the controller parser and the Desktop parser accept
 * (action=requestPeer with peerProfileName/summary). Create-agent requests
 * (request=create-agent and variants) are excluded — they belong to the
 * manager-create affordance, never the handoff path. Never invents names:
 * only bodies already present in the text are returned.
 */
export function handoffStatusTargets(content: string): PeerHandoffTarget[] {
  if (!content) {
    return [];
  }
  const out: PeerHandoffTarget[] = [];
  for (const body of extractStatusBodies(content)) {
    if (decisionAction(body) !== "requestPeer") {
      continue;
    }
    if (isCreateAgentRequestBody(body)) {
      continue;
    }
    const peerProfileName =
      typeof body.peerProfileName === "string"
        ? body.peerProfileName.trim()
        : typeof body.peerProfile === "string"
          ? body.peerProfile.trim()
          : "";
    if (!peerProfileName) {
      continue;
    }
    out.push({
      peerProfileName,
      summary: typeof body.summary === "string" ? body.summary : undefined,
    });
  }
  return out;
}

/** First existing-peer handoff target in the message content, if any. */
export function peerHandoffTargetFromMessage(
  message: JevMessageLike | undefined | null,
): PeerHandoffTarget | null {
  if (!message || typeof message.content !== "string") {
    return null;
  }
  const targets = handoffStatusTargets(message.content);
  return targets.length > 0 ? targets[0] : null;
}

/**
 * True when the message content carries an equivalent existing-peer
 * requestPeer handoff hint (STATUS_JSON with a named peerProfileName,
 * excluding create-agent requests). Covers harness-emitted lines that
 * arrive without the Jev metadata flag.
 */
export function messageHasHandoffHint(message: JevMessageLike | undefined | null): boolean {
  return peerHandoffTargetFromMessage(message) !== null;
}

/**
 * Either handoff signal: the Jev request flag or an equivalent
 * requestPeer STATUS_JSON hint in the content.
 */
export function messageNeedsHandoffAttention(
  message: JevMessageLike | undefined | null,
): boolean {
  return messageNeedsPeerHandoff(message) || messageHasHandoffHint(message);
}

/**
 * Suggest an existing peer profile named in natural-language text.
 * Matches only names from the provided roster (case-insensitive);
 * returns null when nothing matches — never invents a profile name.
 */
export function suggestPeerHandoffTarget(
  content: string,
  availableProfiles: string[] | null | undefined,
): string | null {
  const text = (content || "").toLowerCase();
  if (!text || !Array.isArray(availableProfiles)) {
    return null;
  }
  const candidates = availableProfiles
    .map((name) => (typeof name === "string" ? name.trim() : ""))
    .filter((name) => name.length > 0)
    .sort((a, b) => b.length - a.length);
  for (const name of candidates) {
    const needle = name.toLowerCase();
    // Word-ish match so "scout" matches "hand off to scout" but an
    // empty or partial token never fabricates a target.
    const escaped = needle.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
    if (new RegExp(`(^|[^a-z0-9_-])${escaped}([^a-z0-9_-]|$)`, "i").test(text)) {
      return name;
    }
  }
  return null;
}

/**
 * Wrapper/manager-facing affordance gate: show handoff UI only when a
 * handoff signal is present AND the signed-in principal may act on it.
 * Peers keep the read-only jevIntent caption only — the gate reuses the
 * existing create-agent principal check so no new authority is granted
 * (handoff itself never creates; fulfillment stays on existing
 * coordination: open the named peer's standing thread or use the
 * thread's enabled coordination targets).
 */
export function mayShowHandoffAffordance(
  message: JevMessageLike | undefined | null,
  principal: string | undefined,
  labels?: Record<string, string>,
): boolean {
  return messageNeedsHandoffAttention(message) && isCreateAgentPrincipal(principal, labels);
}

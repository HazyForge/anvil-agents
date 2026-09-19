import {
  isCreateAgentPrincipal,
  parseCreateAgentIntent,
  type CreateAgentInput,
} from "./createAgentPolicy.ts";

/** User-message metadata key set by the API turn path (see docs/jev-intent-routing.md). */
export const JEV_NEEDS_MANAGER_CREATE_METADATA_KEY = "jevNeedsManagerCreate";

/** Turn AgentRun annotation mirroring the metadata flag for kubectl-visible discovery. */
export const JEV_NEEDS_MANAGER_CREATE_ANNOTATION =
  "control.anvil.hazyforge.io/jev-needs-manager-create";

export type JevMessageLike = {
  content?: string;
  metadata?: unknown;
};

function isRecord(value: unknown): value is Record<string, unknown> {
  return Boolean(value) && typeof value === "object" && !Array.isArray(value);
}

/**
 * True when thread detail marks this user message as a classified,
 * non-unclear create_agent_request needing a Wrapper/manager to fulfill.
 * Request flag only — grants no create authority.
 */
export function messageNeedsManagerCreate(message: JevMessageLike | undefined | null): boolean {
  if (!message || !isRecord(message.metadata)) {
    return false;
  }
  return message.metadata[JEV_NEEDS_MANAGER_CREATE_METADATA_KEY] === true;
}

/**
 * True when a turn's AgentRun annotations carry the manager-create request
 * flag. Same request-only semantics as the message metadata flag.
 */
export function runNeedsManagerCreate(
  annotations: Record<string, string> | undefined | null,
): boolean {
  if (!annotations) {
    return false;
  }
  return annotations[JEV_NEEDS_MANAGER_CREATE_ANNOTATION] === "true";
}

/** Latest message in the thread carrying the manager-create request flag. */
export function latestManagerCreateMessage<T extends JevMessageLike>(
  messages: T[] | null | undefined,
): T | undefined {
  if (!Array.isArray(messages)) {
    return undefined;
  }
  for (let i = messages.length - 1; i >= 0; i--) {
    if (messageNeedsManagerCreate(messages[i])) {
      return messages[i];
    }
  }
  return undefined;
}

/**
 * Suggest a CreateAgentPanel prefill from the requesting message text.
 * Uses the existing natural-language parser only — never invents a name:
 * vague requests return null and the panel stays blank for the manager.
 */
export function suggestCreateAgentInput(content: string): CreateAgentInput | null {
  const parsed = parseCreateAgentIntent((content || "").trim());
  return parsed.length > 0 ? parsed[0] : null;
}

/**
 * Manager-facing affordance gate: show the fulfill-create UI only when the
 * message carries the request flag AND the signed-in principal may
 * create-agent (Wrapper/manager). Peers keep the read-only jevIntent
 * caption only — never a create button.
 */
export function mayShowCreateAffordance(
  message: JevMessageLike | undefined | null,
  principal: string | undefined,
  labels?: Record<string, string>,
): boolean {
  return messageNeedsManagerCreate(message) && isCreateAgentPrincipal(principal, labels);
}

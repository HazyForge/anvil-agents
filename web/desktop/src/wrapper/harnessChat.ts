import { appendChatMessageStream, getChatThread, listChatThreads } from "../api/chat";
import { APIError, AUDITOR_GROK_PROFILE, DESKTOP_GROK_PEER_PROFILE } from "../api/client";
import type { ChatChip, ChatMessage, ChatThread } from "../api/types.chat";
import { formatTurnError } from "./turn";

/** Always-on manager harness profiles. Dedicated grok-home — never auditor or desktop-grok-peer. */
export const MANAGER_PROFILE_NAMES = ["desktop-manager", "manager-hazy-trade"] as const;

/** Volume name for the manager pod. Do not reuse auditor or peer homes. */
export const MANAGER_GROK_HOME = "hazy-trade-desktop-manager-grok-home";

/**
 * Tools the manager harness may call. Desktop Chat never invokes them.
 * Hello is text with zero tool calls.
 */
export const MANAGER_HARNESS_TOOLS = ["startSpecialistAgentRun", "requestPeer", "interruptDuplicate"] as const;

export const NO_HARNESS_CHAT =
  "Cannot reach the always-on manager harness chat yet. This Send did not start a run.";

export type HarnessChatLine = {
  id: string;
  kind: "user" | "harness" | "honest";
  content: string;
  chips?: ChatChip[];
  targetAgent?: string;
};

export type HarnessChatResult = {
  text: string;
  source: "harness" | "honest";
  threadId?: string;
  chips?: ChatChip[];
  targetAgent?: string;
};

function isRecord(value: unknown): value is Record<string, unknown> {
  return Boolean(value) && typeof value === "object" && !Array.isArray(value);
}

function isForbiddenManagerProfile(profileName: string | undefined): boolean {
  const name = (profileName || "").trim();
  return (
    name === AUDITOR_GROK_PROFILE ||
    name === DESKTOP_GROK_PEER_PROFILE ||
    name.startsWith("desktop-reqpeer-") ||
    name.startsWith("desktop-peer-") ||
    name.startsWith("desktop-chat-")
  );
}

function assistantLooksLikeTool(message: ChatMessage | undefined): boolean {
  if (!message) {
    return false;
  }
  if ((message.role || "").trim() === "tool") {
    return true;
  }
  const meta = message.metadata;
  if (!isRecord(meta)) {
    return false;
  }
  return Boolean(meta.tool || meta.toolCall || meta.toolCalls || meta.functionCall);
}

export function pickManagerThread(threads: ChatThread[]): ChatThread | undefined {
  const allowed = threads.filter((thread) => !isForbiddenManagerProfile(thread.profileName));
  for (const name of MANAGER_PROFILE_NAMES) {
    const match = allowed.find((thread) => (thread.profileName || "").trim() === name);
    if (match) {
      return match;
    }
  }
  return allowed.find((thread) => /manager/i.test(`${thread.profileName || ""} ${thread.title || ""}`));
}

function linesFromMessages(messages: ChatMessage[]): HarnessChatLine[] {
  const out: HarnessChatLine[] = [];
  for (const message of messages) {
    const content = (message.content || "").trim();
    if (!content || assistantLooksLikeTool(message)) {
      continue;
    }
    const meta = (message.metadata || {}) as Record<string, unknown>;
    const chips = (meta.chips as ChatChip[]) || [];
    const target = typeof meta.targetAgent === "string" ? meta.targetAgent : undefined;
    if (message.role === "user") {
      out.push({ id: message.id || `user-${message.sequence}`, kind: "user", content });
      continue;
    }
    if (message.role === "assistant") {
      out.push({
        id: message.id || `harness-${message.sequence}`,
        kind: "harness",
        content,
        chips: chips.length > 0 ? chips : undefined,
        targetAgent: target,
      });
    }
  }
  return out;
}

/** GET-only. Hydrate Chat from the standing manager thread. Never POSTs AgentRuns. */
export async function loadManagerHarnessHistory(opts: {
  token: string;
  namespace: string;
  signal?: AbortSignal;
}): Promise<{ thread: ChatThread | null; lines: HarnessChatLine[] }> {
  const threads = await listChatThreads(opts.token, opts.namespace, { limit: 50 }, opts.signal);
  const thread = pickManagerThread(threads);
  if (!thread) {
    return { thread: null, lines: [] };
  }
  const detail = await getChatThread(opts.token, opts.namespace, thread.id, opts.signal);
  return { thread, lines: linesFromMessages(detail.messages || []) };
}

/**
 * Proxy an existing standing harness thread as a text stream.
 * Never createAgentRun. Never create a thread. Never execute harness tools.
 * Hello is a user message; the harness replies in text and may choose zero tools.
 */
async function proxyManagerHarnessChat(opts: {
  token: string;
  namespace: string;
  text: string;
  onDelta?: (text: string) => void;
  onChips?: (chips: ChatChip[]) => void;
}): Promise<{ text: string; threadId: string; chips?: ChatChip[]; targetAgent?: string }> {
  const threads = await listChatThreads(opts.token, opts.namespace, { limit: 50 });
  const thread = pickManagerThread(threads);
  if (!thread) {
    throw new APIError(404, "harness_thread_missing", "no standing manager harness chat thread");
  }
  const collectedChips: ChatChip[] = [];
  const posted = await appendChatMessageStream(
    opts.token,
    opts.namespace,
    thread.id,
    { content: opts.text },
    (chunk) => opts.onDelta?.(chunk),
    undefined,
    (chips) => {
      collectedChips.push(...chips);
      opts.onChips?.(chips);
    },
  );
  if (assistantLooksLikeTool(posted.assistant)) {
    throw new APIError(
      502,
      "harness_tool_without_text",
      "manager harness used a tool instead of a text reply; Desktop did not start a run",
    );
  }
  const reply = posted.assistant?.content?.trim() || "";
  if (!reply) {
    throw new APIError(502, "empty_assistant", "manager harness returned an empty assistant reply");
  }
  const meta = (posted.assistant?.metadata || {}) as Record<string, unknown>;
  const targetAgent = typeof meta.targetAgent === "string" ? meta.targetAgent : undefined;
  return {
    text: reply,
    threadId: posted.thread?.id || thread.id,
    chips: collectedChips.length > 0 ? collectedChips : undefined,
    targetAgent,
  };
}

/**
 * Desktop Chat Send. Streams the always-on manager harness reply.
 * Never POSTs AgentRuns. No command parser. No speak() fakes. No tool execution.
 */
export async function streamDesktopChat(opts: {
  token: string;
  namespace: string;
  text: string;
  onDelta?: (text: string) => void;
  onChips?: (chips: ChatChip[]) => void;
}): Promise<HarnessChatResult> {
  const text = opts.text.trim();
  if (!text) {
    return { text: NO_HARNESS_CHAT, source: "honest" };
  }
  try {
    const reply = await proxyManagerHarnessChat({
      token: opts.token,
      namespace: opts.namespace,
      text,
      onDelta: opts.onDelta,
      onChips: opts.onChips,
    });
    return {
      text: reply.text,
      source: "harness",
      threadId: reply.threadId,
      chips: reply.chips,
      targetAgent: reply.targetAgent,
    };
  } catch (err) {
    return { text: `${NO_HARNESS_CHAT} (${formatTurnError(err)})`, source: "honest" };
  }
}

export async function sendDesktopChat(opts: {
  token: string;
  namespace: string;
  text: string;
}): Promise<HarnessChatResult> {
  return streamDesktopChat(opts);
}

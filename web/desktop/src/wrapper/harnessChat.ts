import { appendChatMessage, getChatThread, listChatThreads } from "../api/chat";
import { APIError, AUDITOR_GROK_PROFILE, DESKTOP_GROK_PEER_PROFILE } from "../api/client";
import type { ChatMessage, ChatThread } from "../api/types.chat";
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
};

export type HarnessChatResult = {
  text: string;
  source: "harness" | "honest";
  threadId?: string;
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
    if (message.role === "user") {
      out.push({ id: message.id || `user-${message.sequence}`, kind: "user", content });
      continue;
    }
    if (message.role === "assistant") {
      out.push({ id: message.id || `harness-${message.sequence}`, kind: "harness", content });
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
 * Proxy an existing standing harness thread. Never createAgentRun. Never create a
 * thread (that can provision a Job). Never execute harness tools on Desktop.
 * Hello is a user message; the harness replies in text and may choose zero tools.
 */
async function proxyManagerHarnessChat(opts: {
  token: string;
  namespace: string;
  text: string;
}): Promise<{ text: string; threadId: string }> {
  const threads = await listChatThreads(opts.token, opts.namespace, { limit: 50 });
  const thread = pickManagerThread(threads);
  if (!thread) {
    throw new APIError(404, "harness_thread_missing", "no standing manager harness chat thread");
  }
  const posted = await appendChatMessage(opts.token, opts.namespace, thread.id, {
    content: opts.text,
  });
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
  return { text: reply, threadId: posted.thread?.id || thread.id };
}

/**
 * Desktop Chat Send. Proxies the always-on manager harness chat stream.
 * Never POSTs AgentRuns. No command parser. No speak() fakes. No tool execution.
 */
export async function sendDesktopChat(opts: {
  token: string;
  namespace: string;
  text: string;
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
    });
    return { text: reply.text, source: "harness", threadId: reply.threadId };
  } catch (err) {
    return { text: `${NO_HARNESS_CHAT} (${formatTurnError(err)})`, source: "honest" };
  }
}

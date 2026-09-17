import {
  appendChatMessageStream,
  dedupeChips,
  extractChipsAndCleanText,
  extractChipsFromMessage,
  getChatThread,
  listChatThreads,
  turnStatusFromMessage,
} from "../api/chat";
import { APIError, AUDITOR_GROK_PROFILE, DESKTOP_GROK_PEER_PROFILE } from "../api/client";
import type { ChatChip, ChatMessage, ChatThread, ChatTurnStatus } from "../api/types.chat";
import { formatTurnError } from "./turn";

/** Always-on manager harness profiles. Dedicated grok-home — never auditor or desktop-grok-peer. */
export const MANAGER_PROFILE_NAMES = ["desktop-manager", "manager-hazy-trade"] as const;

/** Volume name for the manager pod. Do not reuse auditor or peer homes. */
export const MANAGER_GROK_HOME = "hazy-trade-desktop-manager-grok-home";

/**
 * Tools the manager harness may call. Desktop Chat never invokes them.
 * Hello is text with zero tool calls.
 */
export const MANAGER_HARNESS_TOOLS = [
  "startSpecialistAgentRun",
  "requestPeer",
  "interruptDuplicate",
  "steerAgentRun",
  "directReply",
] as const;

export const NO_HARNESS_CHAT =
  "Cannot reach the always-on manager harness chat yet. This Send did not start a run.";

export type HarnessChatLine = {
  id: string;
  kind: "user" | "harness" | "honest";
  content: string;
  chips?: ChatChip[];
  status?: ChatTurnStatus;
  targetAgent?: string;
};

export type HarnessChatResult = {
  text: string;
  source: "harness" | "honest";
  threadId?: string;
  chips?: ChatChip[];
  lines?: HarnessChatLine[];
  targetAgent?: string;
};

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

function targetAgentFromMessage(message: ChatMessage): string | undefined {
  if (!message.metadata || typeof message.metadata !== "object") {
    return undefined;
  }
  const meta = message.metadata as Record<string, unknown>;
  return typeof meta.targetAgent === "string" ? meta.targetAgent : undefined;
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

export function waitingLineForUser(userId: string, status: ChatTurnStatus = "waiting"): HarnessChatLine {
  const label = status === "queued" ? "Queued" : status === "running" ? "Running" : status === "failed" ? "Failed" : "Waiting";
  const chipStatus = status === "failed" ? "failed" : status === "running" ? "running" : "pending";
  return {
    id: `pending-${userId}`,
    kind: status === "failed" ? "honest" : "harness",
    content: "",
    status,
    chips: [{ id: `turn-${status}`, type: "transition", label, status: chipStatus }],
  };
}

export function linesFromMessages(messages: ChatMessage[]): HarnessChatLine[] {
  const out: HarnessChatLine[] = [];
  for (const message of messages) {
    const role = (message.role || "").trim().toLowerCase();
    const rawContent = (message.content || "").trim();
    const status = turnStatusFromMessage(message);
    const targetAgent = targetAgentFromMessage(message);

    if (role === "user") {
      if (rawContent) {
        out.push({
          id: message.id || `user-${message.sequence}`,
          kind: "user",
          content: rawContent,
          status,
        });
      }
      continue;
    }

    if (role === "assistant" || role === "tool") {
      const { text: cleanContent, chips: textChips } = extractChipsAndCleanText(rawContent);
      const metaChips = extractChipsFromMessage(message);
      const combinedChips = dedupeChips([...textChips, ...metaChips]);

      if (role === "tool" && out.length > 0 && out[out.length - 1].kind === "harness") {
        const last = out[out.length - 1];
        last.chips = dedupeChips([...(last.chips || []), ...combinedChips]);
        if (!last.content && cleanContent) {
          last.content = cleanContent;
        }
        if (status) {
          last.status = status;
        }
        if (targetAgent && !last.targetAgent) {
          last.targetAgent = targetAgent;
        }
        continue;
      }

      if (!cleanContent && combinedChips.length === 0 && !status) {
        continue;
      }

      out.push({
        id: message.id || `harness-${message.sequence}`,
        kind: "harness",
        content: cleanContent,
        chips: combinedChips.length > 0 ? combinedChips : undefined,
        status,
        targetAgent,
      });
    }
  }
  return withOpenTurnStatus(out);
}

export function withOpenTurnStatus(lines: HarnessChatLine[]): HarnessChatLine[] {
  if (lines.length === 0) {
    return lines;
  }
  const last = lines[lines.length - 1];
  if (last.kind !== "user") {
    return lines;
  }
  if (last.status === "failed") {
    return [...lines, waitingLineForUser(last.id, "failed")];
  }
  if (last.status === "queued") {
    return [...lines, waitingLineForUser(last.id, "queued")];
  }
  if (last.status === "running") {
    return [...lines, waitingLineForUser(last.id, "running")];
  }
  return [...lines, waitingLineForUser(last.id, "waiting")];
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
  onStatus?: (status: ChatTurnStatus) => void;
}): Promise<{ text: string; threadId: string; chips?: ChatChip[]; targetAgent?: string }> {
  const threads = await listChatThreads(opts.token, opts.namespace, { limit: 50 });
  const thread = pickManagerThread(threads);
  if (!thread) {
    throw new APIError(404, "harness_thread_missing", "no standing manager harness chat thread");
  }
  opts.onStatus?.("waiting");
  const posted = await appendChatMessageStream(
    opts.token,
    opts.namespace,
    thread.id,
    { content: opts.text },
    {
      onDelta: (chunk) => {
        opts.onStatus?.("running");
        opts.onDelta?.(chunk);
      },
      onChips: (chips) => {
        opts.onStatus?.("running");
        opts.onChips?.(chips);
      },
    },
  );
  const chips =
    posted.chips && posted.chips.length > 0
      ? posted.chips
      : extractChipsFromMessage(posted.assistant);
  const reply = posted.assistant?.content?.trim() || "";
  if (!reply && chips.length === 0) {
    throw new APIError(502, "empty_assistant", "manager harness returned an empty assistant reply");
  }
  const meta = posted.assistant?.metadata;
  const targetAgent =
    meta && typeof meta === "object" && typeof (meta as { targetAgent?: unknown }).targetAgent === "string"
      ? (meta as { targetAgent: string }).targetAgent
      : undefined;
  return {
    text: reply,
    threadId: posted.thread?.id || thread.id,
    chips: chips.length > 0 ? chips : undefined,
    targetAgent,
  };
}

function recoveredReplyForUser(lines: HarnessChatLine[], userText: string): HarnessChatLine | undefined {
  const wanted = userText.trim();
  for (let i = lines.length - 1; i >= 0; i--) {
    if (lines[i].kind === "user" && lines[i].content.trim() === wanted) {
      const next = lines[i + 1];
      if (next && next.kind === "harness" && !next.id.startsWith("pending-") && (next.content.trim() || (next.chips && next.chips.length > 0))) {
        return next;
      }
      return undefined;
    }
  }
  return undefined;
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
  onStatus?: (status: ChatTurnStatus) => void;
}): Promise<HarnessChatResult> {
  const text = opts.text.trim();
  if (!text) {
    return { text: NO_HARNESS_CHAT, source: "honest" };
  }
  let threadId: string | undefined;
  try {
    opts.onStatus?.("waiting");
    const reply = await proxyManagerHarnessChat({
      token: opts.token,
      namespace: opts.namespace,
      text,
      onDelta: opts.onDelta,
      onChips: opts.onChips,
      onStatus: opts.onStatus,
    });
    threadId = reply.threadId;
    const history = await loadManagerHarnessHistory({ token: opts.token, namespace: opts.namespace }).catch(
      () => null,
    );
    const recovered = history ? recoveredReplyForUser(history.lines, text) : undefined;
    return {
      text: recovered?.content || reply.text,
      source: "harness",
      threadId: history?.thread?.id || reply.threadId,
      chips: recovered?.chips || reply.chips,
      lines: history?.lines,
      targetAgent: recovered?.targetAgent || reply.targetAgent,
    };
  } catch (err) {
    const history = await loadManagerHarnessHistory({ token: opts.token, namespace: opts.namespace }).catch(
      () => null,
    );
    const recovered = history ? recoveredReplyForUser(history.lines, text) : undefined;
    if (recovered) {
      return {
        text: recovered.content,
        source: "harness",
        threadId: history?.thread?.id || threadId,
        chips: recovered.chips,
        lines: history?.lines,
        targetAgent: recovered.targetAgent,
      };
    }
    opts.onStatus?.("failed");
    return {
      text: `${NO_HARNESS_CHAT} (${formatTurnError(err)})`,
      source: "honest",
      threadId: history?.thread?.id || threadId,
      lines: history?.lines,
    };
  }
}

export async function sendDesktopChat(opts: {
  token: string;
  namespace: string;
  text: string;
}): Promise<HarnessChatResult> {
  return streamDesktopChat(opts);
}

export function historyContainsUser(lines: HarnessChatLine[], userText: string): boolean {
  const wanted = userText.trim();
  return lines.some((line) => line.kind === "user" && line.content.trim() === wanted);
}

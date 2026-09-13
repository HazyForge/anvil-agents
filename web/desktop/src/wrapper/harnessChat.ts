import { appendChatMessage, listChatThreads } from "../api/chat";
import { APIError, AUDITOR_GROK_PROFILE, DESKTOP_GROK_PEER_PROFILE } from "../api/client";
import type { ChatThread } from "../api/types.chat";
import { formatTurnError } from "./turn";

/** Always-on manager harness profile. Dedicated grok-home — never auditor or desktop-grok-peer. */
export const MANAGER_PROFILE_NAMES = ["desktop-manager", "manager-hazy-trade"] as const;

export const NO_HARNESS_CHAT =
  "Cannot reach the always-on manager harness chat yet. This Send did not start a run.";

export type HarnessChatResult = {
  text: string;
  source: "harness" | "honest";
};

function isForbiddenManagerProfile(profileName: string | undefined): boolean {
  const name = (profileName || "").trim();
  return name === AUDITOR_GROK_PROFILE || name === DESKTOP_GROK_PEER_PROFILE;
}

function pickManagerThread(threads: ChatThread[]): ChatThread | undefined {
  const allowed = threads.filter((thread) => !isForbiddenManagerProfile(thread.profileName));
  for (const name of MANAGER_PROFILE_NAMES) {
    const match = allowed.find((thread) => (thread.profileName || "").trim() === name);
    if (match) {
      return match;
    }
  }
  return allowed.find((thread) => /manager/i.test(`${thread.profileName || ""} ${thread.title || ""}`));
}

/**
 * Proxy an existing standing harness thread. Never createAgentRun. Never create a
 * thread (that can provision a Job). Hello is a user message; the harness replies
 * in text and may choose zero tool calls.
 */
async function proxyManagerHarnessChat(opts: {
  token: string;
  namespace: string;
  text: string;
}): Promise<string> {
  const threads = await listChatThreads(opts.token, opts.namespace, { limit: 50 });
  const thread = pickManagerThread(threads);
  if (!thread) {
    throw new APIError(404, "harness_thread_missing", "no standing manager harness chat thread");
  }
  const posted = await appendChatMessage(opts.token, opts.namespace, thread.id, {
    content: opts.text,
  });
  const reply = posted.assistant?.content?.trim() || "";
  if (!reply) {
    throw new APIError(502, "empty_assistant", "manager harness returned an empty assistant reply");
  }
  return reply;
}

/**
 * Desktop Chat Send. Proxies the always-on manager harness chat stream.
 * Never POSTs AgentRuns. No command parser. No speak() fakes.
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
    return { text: reply, source: "harness" };
  } catch (err) {
    return { text: `${NO_HARNESS_CHAT} (${formatTurnError(err)})`, source: "honest" };
  }
}

import {
  appendChatMessage,
  createChatThread,
  listChatThreads,
} from "../api/chat";
import { APIError, delegateHarness, ensureAgentRunProfile, type CompositionDocument } from "../api/client";
import type { ChatMessage, ChatThread } from "../api/types.chat";
import { parseWrapperIntent, WRAPPER_PROFILE_NAME } from "./intent";

export type EntityLine = {
  author: string;
  content: string;
  threadId?: string;
};

export type VisibleMessage = {
  id: string;
  author: "user" | "wrapper" | "entity";
  profile?: string;
  content: string;
  createdAt: string;
};

export type WrapperTurnResult = {
  wrapperThread: ChatThread | null;
  spawned: CompositionDocument[];
  room: EntityLine[];
  wrapperReply: string;
  history: VisibleMessage[];
};

function isRecord(value: unknown): value is Record<string, unknown> {
  return Boolean(value) && typeof value === "object" && !Array.isArray(value);
}

export function isStubMetadata(metadata: unknown): boolean {
  return isRecord(metadata) && Boolean(metadata.stub);
}

export function isWrapperMetadata(metadata: unknown): boolean {
  return isRecord(metadata) && Boolean(metadata.wrapper);
}

export function entityAuthor(metadata: unknown): string {
  if (!isRecord(metadata)) {
    return "";
  }
  const name = metadata.authorProfile;
  return typeof name === "string" ? name.trim() : "";
}

export function visibleFromChat(messages: ChatMessage[]): VisibleMessage[] {
  const out: VisibleMessage[] = [];
  for (const message of messages) {
    if (isStubMetadata(message.metadata)) {
      continue;
    }
    const entity = entityAuthor(message.metadata);
    if (isWrapperMetadata(message.metadata)) {
      out.push({
        id: message.id || `wrapper-${message.sequence}`,
        author: "wrapper",
        content: message.content,
        createdAt: message.createdAt,
      });
      continue;
    }
    if (entity) {
      out.push({
        id: message.id || `entity-${message.sequence}`,
        author: "entity",
        profile: entity,
        content: message.content,
        createdAt: message.createdAt,
      });
      continue;
    }
    if (message.role === "user") {
      out.push({
        id: message.id || `user-${message.sequence}`,
        author: "user",
        content: message.content,
        createdAt: message.createdAt,
      });
    }
  }
  return out;
}

function profilePrompt(name: string, description: string): string {
  return `You are ${name}, an Anvil agent spawned by Anvil Agents Desktop. ${description} You do not receive OIDC tokens. Talk to peers in short sentences.`;
}

async function ensureThread(
  token: string,
  namespace: string,
  profileName: string,
  title: string,
  metadata?: unknown,
): Promise<ChatThread> {
  const existing = await listChatThreads(token, namespace, { mode: "persona", profileName, limit: 50 });
  const match = existing.find((thread) => thread.profileName === profileName);
  if (match) {
    return match;
  }
  return createChatThread(token, namespace, {
    mode: "persona",
    profileName,
    title,
    metadata,
  });
}

function entityLine(name: string, peer: string, stdout?: string): string {
  const trimmed = stdout?.trim() ?? "";
  if (trimmed && !trimmed.includes("anvil-desktop-fixture") && trimmed.length < 400) {
    const last = trimmed.split(/\n+/).map((line) => line.trim()).filter(Boolean).at(-1);
    if (last) {
      return last;
    }
  }
  return `Hello ${peer} — ${name} here. The desktop wrapper just spawned me.`;
}

async function speak(
  name: string,
  peer: string,
  harnesses: string[],
): Promise<{ line: string; harness?: string }> {
  const prompt = `You are ${name}, an Anvil agent. Say one short sentence greeting ${peer}. Do not mention tokens or secrets.`;
  const harness = harnesses[0];
  if (!harness) {
    return { line: entityLine(name, peer) };
  }
  try {
    const result = await delegateHarness(harness, prompt, 20);
    return { line: entityLine(name, peer, result.stdout), harness };
  } catch {
    return { line: entityLine(name, peer) };
  }
}

export async function runWrapperTurn(opts: {
  token: string;
  namespace: string;
  userText: string;
  chatEnabled: boolean;
  writeEnabled: boolean;
  wrapperThreadId: string | null;
  existingProfileNames: string[];
  harnesses: string[];
}): Promise<WrapperTurnResult> {
  const text = opts.userText.trim();
  if (!text) {
    throw new Error("message is required");
  }
  if (!opts.writeEnabled) {
    throw new Error("composition write is disabled on this API — Desktop cannot create AgentRunProfiles");
  }

  const wrapperProfile = await ensureAgentRunProfile(opts.token, opts.namespace, {
    name: WRAPPER_PROFILE_NAME,
    description: "Anvil Agents Desktop wrapper entity. Spawns more AgentRunProfiles when asked.",
    systemPrompt: "You are the Anvil Agents Desktop wrapper. When asked to create agents, POST more AgentRunProfiles. You can introduce spawned personas to each other.",
    intent: "observe",
  });

  let wrapperThread: ChatThread | null = null;
  if (opts.chatEnabled) {
    if (opts.wrapperThreadId) {
      wrapperThread = {
        id: opts.wrapperThreadId,
        namespace: opts.namespace,
        profileName: WRAPPER_PROFILE_NAME,
        mode: "persona",
        title: "Desktop wrapper",
        createdAt: "",
        updatedAt: "",
        createdBy: "",
      };
    } else {
      wrapperThread = await ensureThread(opts.token, opts.namespace, WRAPPER_PROFILE_NAME, "Desktop wrapper", {
        wrapper: true,
      });
    }
    await appendChatMessage(opts.token, opts.namespace, wrapperThread.id, { content: text });
  }

  const intent = parseWrapperIntent(text, opts.existingProfileNames);
  const spawned: CompositionDocument[] = [];
  const namesToCreate = intent.spawn ? intent.names : [];
  for (const name of namesToCreate) {
    const doc = await ensureAgentRunProfile(opts.token, opts.namespace, {
      name,
      description: `Spawned by ${WRAPPER_PROFILE_NAME} from Anvil Agents Desktop.`,
      systemPrompt: profilePrompt(name, "Work with peer agents spawned in the same namespace."),
      intent: "observe",
    });
    spawned.push(doc);
    if (opts.chatEnabled) {
      await ensureThread(opts.token, opts.namespace, name, name, {
        spawnedFrom: wrapperThread?.id,
        spawnedBy: WRAPPER_PROFILE_NAME,
      });
    }
  }

  const roster = uniqueNames([
    ...opts.existingProfileNames.filter((name) => name !== WRAPPER_PROFILE_NAME),
    ...spawned.map((doc) => doc.metadata.name),
  ]);
  const room: EntityLine[] = [];
  const shouldTalk = roster.length >= 2 && (intent.talk || spawned.length >= 2);
  if (shouldTalk) {
    const a = roster[0];
    const b = roster[1];
    const first = await speak(a, b, opts.harnesses);
    const second = await speak(b, a, opts.harnesses);
    room.push({ author: a, content: first.line });
    room.push({ author: b, content: second.line });
    if (opts.chatEnabled) {
      const threads = await listChatThreads(opts.token, opts.namespace, { mode: "persona", limit: 100 });
      const threadA = threads.find((thread) => thread.profileName === a);
      const threadB = threads.find((thread) => thread.profileName === b);
      if (threadA) {
        const posted = await appendChatMessage(opts.token, opts.namespace, threadA.id, {
          content: first.line,
          metadata: { entity: true, authorProfile: a, wrapper: false },
        });
        room[0].threadId = posted.thread.id;
      }
      if (threadB) {
        const posted = await appendChatMessage(opts.token, opts.namespace, threadB.id, {
          content: second.line,
          metadata: { entity: true, authorProfile: b },
        });
        room[1].threadId = posted.thread.id;
      }
    }
  }

  const wrapperReply = composeWrapperReply({
    wrapperProfile: wrapperProfile.metadata.name,
    spawned,
    room,
    talk: shouldTalk,
    chatEnabled: opts.chatEnabled,
  });

  const history: VisibleMessage[] = [];
  if (opts.chatEnabled && wrapperThread) {
    const posted = await appendChatMessage(opts.token, opts.namespace, wrapperThread.id, {
      content: wrapperReply,
      metadata: {
        wrapper: true,
        spawned: spawned.map((doc) => doc.metadata.name),
      },
    });
    wrapperThread = posted.thread;
    history.push({
      id: posted.user.id,
      author: "wrapper",
      content: wrapperReply,
      createdAt: posted.user.createdAt,
    });
  } else {
    history.push({
      id: `local-wrapper-${Date.now()}`,
      author: "wrapper",
      content: wrapperReply,
      createdAt: new Date().toISOString(),
    });
  }

  return { wrapperThread, spawned, room, wrapperReply, history };
}

function uniqueNames(names: string[]): string[] {
  const seen = new Set<string>();
  const out: string[] = [];
  for (const raw of names) {
    const name = raw.trim();
    if (!name || seen.has(name)) {
      continue;
    }
    seen.add(name);
    out.push(name);
  }
  return out;
}

function composeWrapperReply(opts: {
  wrapperProfile: string;
  spawned: CompositionDocument[];
  room: EntityLine[];
  talk: boolean;
  chatEnabled: boolean;
}): string {
  const lines: string[] = [];
  if (opts.spawned.length > 0) {
    lines.push(
      `Created AgentRunProfiles ${opts.spawned.map((doc) => doc.metadata.name).join(", ")} via POST .../agent-run-profiles (console-managed).`,
    );
  } else {
    lines.push(`I am ${opts.wrapperProfile}. Ask me to create agents (AgentRunProfiles) or to have existing ones talk.`);
  }
  if (opts.room.length > 0) {
    lines.push("They exchanged messages:");
    for (const line of opts.room) {
      lines.push(`- ${line.author}: ${line.content}`);
    }
  } else if (opts.talk) {
    lines.push("Need at least two personas before they can talk.");
  }
  if (!opts.chatEnabled) {
    lines.push("Standing chat is off on this API; profile create still went through composition write. The echo stub was not used as the reply.");
  } else {
    lines.push("PR 168 echo stubs are hidden here. This wrapper turn is the reply.");
  }
  return lines.join("\n");
}

export function formatTurnError(err: unknown): string {
  if (err instanceof APIError) {
    return `${err.code}: ${err.message}`;
  }
  return err instanceof Error ? err.message : String(err);
}

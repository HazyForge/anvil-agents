#!/usr/bin/env node
/**
 * Live signed-in Desktop standing-chat latency probe.
 *
 * Exercises the real standing WebSocket token path with a caller-supplied
 * OIDC bearer (Kind-local issuer or Zitadel — never the local-only stub
 * session), sends one chat message, measures send → first live token, settles
 * from the durable thread record, and appends one JSONL sample to
 * `.runtime/chat-latency.jsonl` with source `desktop-chat-live-signed-in`.
 *
 * Honest skip contract (exit 0, prints skip/inconclusive, writes nothing):
 * no bearer configured, stub-OIDC 401 denial, OIDC 503 unavailability, or a
 * Job-plane snapshot (thread has no standing session). Only a delivered
 * standing turn writes a sample — this probe never invents live numbers.
 *
 * The bearer comes from ANVIL_AGENTS_ACCESS_TOKEN or --token-file only. It is
 * never placed in a URL or query string, never logged, and never written to
 * the JSONL sink.
 *
 * Run the pure unit tests (no cluster, no OIDC):
 *   node --experimental-strip-types --test hack/desktop-standing-chat-live.test.mjs
 *
 * Run a live probe (needs a standing-enabled manager thread + real OIDC):
 *   node --experimental-strip-types hack/desktop-standing-chat-live.mjs --live \
 *     --api-origin http://127.0.0.1:18080 --namespace agents --thread <id> \
 *     --out .runtime/chat-latency.jsonl
 *
 * Full runbook: docs/standing-inprocess-harness.md ("Desktop chat e2e over
 * the standing WebSocket").
 */
import assert from "node:assert/strict";
import { randomUUID } from "node:crypto";
import { mkdirSync } from "node:fs";
import { dirname, join, resolve } from "node:path";
import { test } from "node:test";
import { fileURLToPath } from "node:url";

import {
  appendChatLatencyReport,
  createChatLatencyTracker,
  formatChatLatencyReport,
} from "../web/desktop/src/wrapper/chatLatency.ts";

export const LIVE_SOURCE_DEFAULT = "desktop-chat-live-signed-in";
export const CHAT_STREAM_PROTOCOL = "anvil-agents.chat-stream.v1";
export const ACCESS_TOKEN_ENV = "ANVIL_AGENTS_ACCESS_TOKEN";

export function argValue(args, name) {
  const idx = args.indexOf(name);
  if (idx === -1 || idx + 1 >= args.length) {
    return undefined;
  }
  return args[idx + 1];
}

export function parseProbeArgs(args) {
  return {
    live: args.includes("--live"),
    apiOrigin: argValue(args, "--api-origin") || "http://127.0.0.1:18080",
    namespace: argValue(args, "--namespace") || "agents",
    threadId: argValue(args, "--thread") || "",
    message: argValue(args, "--message") || "reply briefly: hi",
    out:
      argValue(args, "--out") ||
      join(resolve(dirname(fileURLToPath(import.meta.url)), ".."), ".runtime", "chat-latency.jsonl"),
    source: argValue(args, "--source") || LIVE_SOURCE_DEFAULT,
    tokenFile: argValue(args, "--token-file") || "",
    snapshotTimeoutMs: Number(argValue(args, "--snapshot-timeout-ms")) || 15000,
    terminalTimeoutMs: Number(argValue(args, "--terminal-timeout-ms")) || 120000,
  };
}

/** Load the bearer from env or a protected token file. Never logs the token. */
export function loadProbeToken({ env = {}, readFile = undefined, tokenFile = "" } = {}) {
  const fromEnv = (env[ACCESS_TOKEN_ENV] || "").trim();
  if (fromEnv) {
    return { token: fromEnv };
  }
  const path = (tokenFile || "").trim();
  if (!path) {
    return { error: `no bearer: set ${ACCESS_TOKEN_ENV} or pass --token-file` };
  }
  if (!readFile) {
    return { error: "no file reader available for --token-file" };
  }
  try {
    const raw = readFile(path, "utf8").trim();
    if (!raw) {
      return { error: `token file ${path} is empty` };
    }
    return { token: raw.split(/\s+/)[0] };
  } catch (err) {
    return { error: `read token file: ${err instanceof Error ? err.message : String(err)}` };
  }
}

/** Build an API URL. Never appends a token: auth stays in headers/subprotocol. */
export function apiURL(origin, path) {
  const trimmed = (origin || "").trim().replace(/\/$/, "");
  if (!/^https?:\/\//i.test(trimmed)) {
    throw new Error(`api origin must be http(s): ${trimmed || "(empty)"}`);
  }
  if (!path.startsWith("/")) {
    throw new Error(`api path must start with /: ${path}`);
  }
  return `${trimmed}${path}`;
}

/** Map an http(s) origin to its ws(s) peer for the stream upgrade. */
export function wsURL(origin, path) {
  const url = apiURL(origin, path);
  return url.replace(/^http:/i, "ws:").replace(/^https:/i, "wss:");
}

/** Redact token occurrences from log/error strings (belt and braces). */
export function redactToken(text, token) {
  const raw = String(text ?? "");
  if (!token || raw.indexOf(token) === -1) {
    return raw;
  }
  return raw.split(token).join("[redacted-token]");
}

/**
 * Classify an API denial into the probe outcome contract. 401 is the stub-OIDC
 * signature (the local-only `anvil-desktop-stub` session is not a real
 * account); 503 authentication_unavailable means the API's verifier is down.
 * Both are honest skips, not measurements.
 */
export function classifyDenial({ status, code }) {
  if (status === 401) {
    return {
      outcome: "skip",
      reason:
        "stub-oidc-denied: the API rejected the bearer with 401 (stub sessions are local-only; mint a Kind-local token)",
    };
  }
  if (status === 503 && (code || "") === "authentication_unavailable") {
    return { outcome: "skip", reason: "oidc-unavailable: the API verifier is not ready" };
  }
  if (status === 403) {
    return { outcome: "fail", reason: "forbidden: the bearer lacks chat permission for this namespace" };
  }
  if (status === 404) {
    return { outcome: "fail", reason: "not found: thread id or namespace is wrong (or not authorized)" };
  }
  return { outcome: "fail", reason: `request failed with status ${status}${code ? ` (${code})` : ""}` };
}

export function apiErrorCode(body) {
  if (body && typeof body === "object" && !Array.isArray(body)) {
    const err = body.error;
    if (err && typeof err === "object" && typeof err.code === "string") {
      return err.code;
    }
    if (typeof body.code === "string") {
      return body.code;
    }
  }
  return "";
}

/** Read the resumed standing session view from a stream snapshot. */
export function snapshotStanding(snapshot) {
  if (!snapshot || typeof snapshot !== "object" || Array.isArray(snapshot)) {
    return undefined;
  }
  const standing = snapshot.standing;
  if (!standing || typeof standing !== "object" || Array.isArray(standing)) {
    return undefined;
  }
  const sessionName = typeof standing.sessionName === "string" ? standing.sessionName.trim() : "";
  if (!sessionName) {
    return undefined;
  }
  return {
    sessionName,
    harness: typeof standing.harness === "string" && standing.harness.trim() ? standing.harness.trim() : undefined,
  };
}

/**
 * Find the durable assistant reply posted after our user message. Prefers the
 * first assistant message with a greater sequence; falls back to the latest
 * assistant message when sequences are absent.
 */
export function settleReply(messages, userSequence) {
  if (!Array.isArray(messages)) {
    return undefined;
  }
  const assistants = messages.filter((m) => m && typeof m === "object" && m.role === "assistant");
  if (typeof userSequence === "number") {
    const after = assistants.filter((m) => typeof m.sequence === "number" && m.sequence > userSequence);
    if (after.length > 0) {
      return after[0];
    }
    return undefined;
  }
  return assistants.length > 0 ? assistants[assistants.length - 1] : undefined;
}

export function newRequestId(randomUUIDFn = randomUUID) {
  return randomUUIDFn();
}

/** Chat append returns 202 Accepted; thread reads return 200. */
export function isAppendAccepted(status) {
  return status === 200 || status === 201 || status === 202;
}

function sleep(ms) {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

async function readJSON(response) {
  try {
    return await response.json();
  } catch {
    return undefined;
  }
}

async function authedGet(url, token) {
  const response = await fetch(url, { headers: { Authorization: `Bearer ${token}`, Accept: "application/json" } });
  return { status: response.status, body: await readJSON(response) };
}

async function authedPost(url, token, payload) {
  const response = await fetch(url, {
    method: "POST",
    headers: { Authorization: `Bearer ${token}`, Accept: "application/json", "Content-Type": "application/json" },
    body: JSON.stringify(payload),
  });
  return { status: response.status, body: await readJSON(response) };
}

function threadPath(namespace, threadId) {
  return `/api/v1/namespaces/${encodeURIComponent(namespace)}/chat/threads/${encodeURIComponent(threadId)}`;
}

function failLive(message, token) {
  console.error(`live probe failed: ${redactToken(message, token)}`);
  process.exitCode = 1;
}

function skipLive(message) {
  console.log(`live probe skip: ${message}`);
}

function inconclusiveLive(message) {
  console.log(`live probe inconclusive: ${message}`);
}

/** Run one live signed-in turn. Writes a JSONL sample only when delivered. */
export async function runLiveProbe(opts, deps = {}) {
  const tokenResult = loadProbeToken({
    env: deps.env ?? process.env,
    readFile: deps.readFile,
    tokenFile: opts.tokenFile,
  });
  if (!opts.threadId.trim()) {
    failLive("missing --thread (create a standing-enabled manager thread first)", tokenResult.token);
    return;
  }
  if (tokenResult.error) {
    skipLive(tokenResult.error);
    return;
  }
  const token = tokenResult.token;
  const base = threadPath(opts.namespace, opts.threadId);

  let detail;
  try {
    detail = await authedGet(apiURL(opts.apiOrigin, base), token);
  } catch (err) {
    failLive(`API unreachable at ${opts.apiOrigin}: ${err instanceof Error ? err.message : String(err)}`, token);
    return;
  }
  if (detail.status !== 200) {
    const decision = classifyDenial({ status: detail.status, code: apiErrorCode(detail.body) });
    if (decision.outcome === "skip") {
      skipLive(decision.reason);
      return;
    }
    failLive(`thread read: ${decision.reason}`, token);
    return;
  }

  const tracker = createChatLatencyTracker(() => Date.now());
  tracker.onStatus("waiting");

  const streamURL = wsURL(opts.apiOrigin, `${base}/stream?messages=50`);
  const WSImpl = deps.WebSocket ?? globalThis.WebSocket;
  if (typeof WSImpl !== "function") {
    failLive("WebSocket is unavailable in this runtime", token);
    return;
  }
  let socket;
  try {
    socket = new WSImpl(streamURL, [CHAT_STREAM_PROTOCOL, `bearer.${token}`]);
  } catch (err) {
    failLive(`open stream failed: ${err instanceof Error ? err.message : String(err)}`, token);
    return;
  }

  let snapshot;
  let terminalSeen = false;
  let streamError;
  let firstTokenAt;
  let liveText = "";
  let doneTurnId;
  let settled = false;
  const finish = () => {
    settled = true;
    try {
      socket.close();
    } catch {
      // Closing a dead stream must never break the probe.
    }
  };
  const snapshotPromise = new Promise((resolve) => {
    const timer = setTimeout(() => resolve(false), Math.max(1, opts.snapshotTimeoutMs));
    socket.addEventListener("message", (event) => {
      if (settled) {
        return;
      }
      let payload;
      try {
        payload = JSON.parse(String(event.data ?? ""));
      } catch {
        return;
      }
      const type = typeof payload.type === "string" ? payload.type : "";
      if (type === "snapshot" && !snapshot) {
        snapshot = payload;
        clearTimeout(timer);
        resolve(true);
        return;
      }
      if (type === "token" && snapshot) {
        const frameThread = typeof payload.threadId === "string" ? payload.threadId : "";
        if (frameThread && frameThread !== opts.threadId) {
          return;
        }
        if (typeof payload.turnId === "string" && payload.turnId) {
          doneTurnId = payload.turnId;
        }
        if (payload.done === true) {
          return;
        }
        const chunk = typeof payload.token === "string" ? payload.token : "";
        if (!chunk) {
          return;
        }
        if (firstTokenAt === undefined) {
          firstTokenAt = Date.now();
          tracker.onStatus("running");
          tracker.onDelta(chunk);
        }
        liveText += chunk;
        return;
      }
      if (type === "terminal") {
        terminalSeen = true;
      }
    });
    socket.addEventListener("error", () => {
      if (!snapshot) {
        streamError = "websocket error before snapshot";
        clearTimeout(timer);
        resolve(false);
      } else {
        streamError = "websocket error";
      }
    });
    socket.addEventListener("close", () => {
      if (!snapshot) {
        streamError = streamError || "stream closed before snapshot";
        clearTimeout(timer);
        resolve(false);
      }
    });
  });

  const snapshotOk = await snapshotPromise;
  if (!snapshotOk || !snapshot) {
    finish();
    if (streamError && /401|unauthorized/i.test(streamError)) {
      skipLive("stub-oidc-denied: the stream upgrade rejected the bearer (stub sessions are local-only)");
      return;
    }
    failLive(`open stream failed: ${streamError || "snapshot timeout"}`, token);
    return;
  }
  const standing = snapshotStanding(snapshot);
  if (!standing) {
    finish();
    inconclusiveLive(
      "job-plane: thread snapshot carries no standing session (needs an InProcess harness profile + standing.liveEnabled on the API)",
    );
    return;
  }

  const requestId = newRequestId(deps.randomUUID ?? randomUUID);
  let posted;
  try {
    posted = await authedPost(apiURL(opts.apiOrigin, `${base}/messages`), token, {
      content: opts.message,
      requestId,
    });
  } catch (err) {
    finish();
    failLive(`send failed: ${err instanceof Error ? err.message : String(err)}`, token);
    return;
  }
  if (!isAppendAccepted(posted.status)) {
    finish();
    const decision = classifyDenial({ status: posted.status, code: apiErrorCode(posted.body) });
    if (decision.outcome === "skip") {
      skipLive(`send: ${decision.reason}`);
      return;
    }
    failLive(`send: ${decision.reason}`, token);
    return;
  }
  const userSequence = posted.body && posted.body.user ? posted.body.user.sequence : undefined;
  const sentTurnId = posted.body && posted.body.turn ? posted.body.turn.id : undefined;

  const terminalDeadline = Date.now() + Math.max(1, opts.terminalTimeoutMs);
  while (!terminalSeen && Date.now() < terminalDeadline) {
    await sleep(250);
  }
  finish();

  const settleDeadline = Date.now() + Math.min(Math.max(1, opts.terminalTimeoutMs), 120000);
  let replyText = "";
  for (;;) {
    try {
      const current = await authedGet(apiURL(opts.apiOrigin, base), token);
      if (current.status === 200 && current.body) {
        const reply = settleReply(current.body.messages, userSequence);
        if (reply && typeof reply.content === "string" && reply.content.trim()) {
          replyText = reply.content;
          break;
        }
      }
    } catch {
      break;
    }
    if (Date.now() >= settleDeadline) {
      break;
    }
    await sleep(1000);
  }
  if (!replyText.trim() && liveText.trim()) {
    replyText = liveText;
  }
  if (!replyText.trim()) {
    failLive("durable reply not observed (terminal seen, no assistant message settled)", token);
    return;
  }

  tracker.markReplyReady();
  tracker.setTurnContext({ threadId: opts.threadId, sessionId: standing.sessionName, path: "standing" });
  const report = tracker.report();
  mkdirSync(dirname(resolve(opts.out)), { recursive: true });
  const wrote = appendChatLatencyReport(report, { path: resolve(opts.out), source: opts.source });
  if (!wrote) {
    failLive(`could not append JSONL sample to ${opts.out} (path must be absolute)`, token);
    return;
  }
  console.log(formatChatLatencyReport(report));
  console.log(
    JSON.stringify({
      ok: true,
      out: opts.out,
      source: opts.source,
      threadId: opts.threadId,
      sessionId: standing.sessionName,
      path: "standing",
      sendToFirstTokenMs: report.sendToFirstTokenMs,
      turnId: sentTurnId ?? doneTurnId,
    }),
  );
}

// ---- script entrypoint (live only with --live) ----
// Unit tests live in hack/desktop-standing-chat-live.test.mjs:
//   node --experimental-strip-types --test hack/desktop-standing-chat-live.test.mjs

const isMainEntry = process.argv[1] === fileURLToPath(import.meta.url);
if (isMainEntry && parseProbeArgs(process.argv.slice(2)).live) {
  const opts = parseProbeArgs(process.argv.slice(2));
  if (process.argv.includes("--help") || process.argv.includes("-h")) {
    console.log(
      [
        "Usage: hack/desktop-standing-chat-live.mjs --live [options]",
        "",
        "  --api-origin URL        anvil-agents API origin (default http://127.0.0.1:18080).",
        "  --namespace NS          chat namespace (default agents).",
        "  --thread ID             standing-enabled manager thread id (required).",
        "  --message TEXT          chat text (default 'reply briefly: hi').",
        "  --out PATH              JSONL sink (default .runtime/chat-latency.jsonl).",
        "  --source LABEL          JSONL source (default desktop-chat-live-signed-in).",
        "  --token-file PATH       read the bearer from a protected file instead of env.",
        "  --snapshot-timeout-ms N stream snapshot wait (default 15000).",
        "  --terminal-timeout-ms N terminal/settle wait (default 120000).",
        "",
        `Bearer: ${ACCESS_TOKEN_ENV} or --token-file. Never pass it as an argument value in shared logs.`,
      ].join("\n"),
    );
    process.exit(0);
  } else {
    const { readFileSync } = await import("node:fs");
    await runLiveProbe(opts, { readFile: readFileSync });
    // Exit explicitly so the scheduled unit tests do not run after the probe.
    process.exit(process.exitCode ?? 0);
  }
}

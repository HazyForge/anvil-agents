import type {
  ChatChip,
  ChatChipStatus,
  ChatChipType,
  ChatMessage,
  ChatToolCall,
  ChatTurnStatus,
} from "./types.chat";

export function normalizeChatMessages(messages: ChatMessage[] | null | undefined): ChatMessage[] {
  return Array.isArray(messages) ? messages : [];
}

export function isRecord(value: unknown): value is Record<string, unknown> {
  return Boolean(value) && typeof value === "object" && !Array.isArray(value);
}

export function peerTargetFromArgs(args: Record<string, unknown>): string {
  return String(
    args.peerProfileName || args.peer || args.targetProfile || args.targetAgent || args.agent || "",
  ).trim();
}

export function turnStatusFromRaw(raw: string | undefined): ChatTurnStatus | undefined {
  const value = (raw || "").trim().toLowerCase();
  if (!value) {
    return undefined;
  }
  if (value.includes("fail") || value.includes("error")) {
    return "failed";
  }
  if (value.includes("queue") || value === "pending") {
    return "queued";
  }
  if (value.includes("wait")) {
    return "waiting";
  }
  if (value.includes("run") || value.startsWith("start") || value.includes("stream") || value === "composing") {
    return "running";
  }
  return undefined;
}

export function turnStatusFromMessage(message: ChatMessage | undefined): ChatTurnStatus | undefined {
  if (!message) {
    return undefined;
  }
  if (!isRecord(message.metadata)) {
    return undefined;
  }
  return (
    turnStatusFromRaw(String(message.metadata.phase || "")) ||
    turnStatusFromRaw(String(message.metadata.status || "")) ||
    turnStatusFromRaw(String(message.metadata.state || ""))
  );
}

export function assistantAfterUser(messages: ChatMessage[], userContent: string): ChatMessage | undefined {
  const wanted = userContent.trim();
  if (!wanted) {
    return undefined;
  }
  let lastUser = -1;
  for (let i = messages.length - 1; i >= 0; i--) {
    if ((messages[i].role || "").toLowerCase() === "user" && (messages[i].content || "").trim() === wanted) {
      lastUser = i;
      break;
    }
  }
  if (lastUser < 0) {
    return undefined;
  }
  for (let i = lastUser + 1; i < messages.length; i++) {
    const role = (messages[i].role || "").toLowerCase();
    if (role === "assistant") {
      return messages[i];
    }
  }
  return undefined;
}

export function dedupeChips(chips: ChatChip[]): ChatChip[] {
  const seen = new Set<string>();
  const out: ChatChip[] = [];
  for (const c of chips) {
    const key = `${c.type}:${c.label.trim().toLowerCase()}`;
    if (!seen.has(key)) {
      seen.add(key);
      out.push(c);
    }
  }
  const order: Record<ChatChipType, number> = {
    routing: 1,
    recipient: 2,
    target: 2,
    transition: 3,
    lifecycle: 3,
    tool: 4,
  };
  return out.sort((a, b) => (order[a.type] || 99) - (order[b.type] || 99));
}

export function extractToolCalls(payload: unknown): ChatToolCall[] {
  if (!isRecord(payload)) {
    return [];
  }
  const out: ChatToolCall[] = [];
  const type = String(payload.type || "").toLowerCase();

  if (type === "function_call" || type === "tool_call" || type === "tool") {
    let args = payload.arguments || payload.args || payload.parameters;
    if (typeof args === "string") {
      try {
        args = JSON.parse(args);
      } catch {
        /* ignore */
      }
    }
    const name =
      typeof payload.name === "string"
        ? payload.name
        : typeof payload.tool === "string"
        ? payload.tool
        : undefined;
    if (name) {
      out.push({
        id: typeof payload.id === "string" ? payload.id : undefined,
        name,
        args: isRecord(args) ? args : undefined,
      });
    }
  }

  if (Array.isArray(payload.toolCalls)) {
    for (const tc of payload.toolCalls) {
      if (isRecord(tc)) {
        out.push(tc as ChatToolCall);
      }
    }
  }
  if (Array.isArray(payload.tool_calls)) {
    for (const tc of payload.tool_calls) {
      if (isRecord(tc)) {
        const fn = isRecord(tc.function) ? tc.function : tc;
        let args = fn.arguments || fn.args;
        if (typeof args === "string") {
          try {
            args = JSON.parse(args);
          } catch {
            /* ignore */
          }
        }
        out.push({
          id: typeof tc.id === "string" ? tc.id : undefined,
          name: typeof fn.name === "string" ? fn.name : undefined,
          args: isRecord(args) ? args : undefined,
        });
      }
    }
  }
  if (isRecord(payload.toolCall)) {
    out.push(payload.toolCall as ChatToolCall);
  }
  if (isRecord(payload.functionCall) || isRecord(payload.function_call)) {
    const fn = (payload.functionCall || payload.function_call) as Record<string, unknown>;
    let args = fn.arguments || fn.args;
    if (typeof args === "string") {
      try {
        args = JSON.parse(args);
      } catch {
        /* ignore */
      }
    }
    out.push({
      name: typeof fn.name === "string" ? fn.name : undefined,
      args: isRecord(args) ? args : undefined,
    });
  }
  if (typeof payload.tool === "string" && payload.tool.trim() && type !== "tool") {
    out.push({
      name: payload.tool.trim(),
      args: isRecord(payload.args)
        ? payload.args
        : isRecord(payload.parameters)
        ? payload.parameters
        : undefined,
    });
  }
  return out;
}

export function extractChipsFromPayload(payload: unknown, eventName?: string): ChatChip[] {
  if (!isRecord(payload)) {
    return [];
  }
  const chips: ChatChip[] = [];

  // Check if chips array already exists
  if (Array.isArray(payload.chips)) {
    for (const c of payload.chips) {
      if (isRecord(c) && typeof c.label === "string" && c.label.trim()) {
        chips.push({
          id: String(c.id || `chip-${chips.length}-${Date.now()}`),
          type: (c.type as ChatChipType) || "transition",
          label: c.label.trim(),
          status: c.status as ChatChipStatus | undefined,
        });
      }
    }
  }

  // 1. Tool calls
  const toolCalls = extractToolCalls(payload);
  for (const tc of toolCalls) {
    const name = (tc.name || tc.tool || "").trim();
    const args = isRecord(tc.args)
      ? tc.args
      : isRecord(tc.arguments)
      ? tc.arguments
      : isRecord(tc.parameters)
      ? tc.parameters
      : {};

    if (name === "startSpecialistAgentRun") {
      chips.push({ id: "routing-delegate", type: "routing", label: "Routing: Delegate" });
      const target = String(args.profileName || args.agent || args.targetAgent || "").trim();
      if (target) {
        chips.push({ id: `to-${target}`, type: "recipient", label: `To: ${target}` });
      }
      const runName = String(args.runName || args.generateName || "").trim();
      if (runName) {
        chips.push({
          id: `run-${runName}`,
          type: "transition",
          label: `Starting AgentRun: ${runName}`,
          status: "running",
        });
      }
      chips.push({ id: "trans-dispatched", type: "transition", label: "Dispatched", status: "completed" });
    } else if (name === "steerAgentRun") {
      chips.push({ id: "routing-steer", type: "routing", label: "Routing: Steer" });
      const target = String(args.runName || args.targetAgent || args.agent || "").trim();
      if (target) {
        chips.push({ id: `to-${target}`, type: "recipient", label: `To: ${target}` });
      }
      chips.push({ id: "trans-dispatched", type: "transition", label: "Dispatched", status: "completed" });
    } else if (name === "requestPeer") {
      chips.push({ id: "routing-request-peer", type: "routing", label: "Routing: requestPeer" });
      const target = peerTargetFromArgs(args);
      if (target) {
        chips.push({ id: `to-${target}`, type: "recipient", label: `To: ${target}` });
      }
      const peerRun = String(args.peerRunName || args.runName || "").trim();
      if (peerRun) {
        chips.push({
          id: `run-${peerRun}`,
          type: "transition",
          label: `Peer run: ${peerRun}`,
          status: "running",
        });
      }
      chips.push({ id: "trans-dispatched", type: "transition", label: "Dispatched", status: "completed" });
    } else if (name === "interruptDuplicate") {
      chips.push({ id: "routing-interrupt", type: "routing", label: "Routing: interruptDuplicate" });
      const target = String(args.duplicateRunName || args.runName || args.targetAgent || "").trim();
      if (target) {
        chips.push({ id: `to-${target}`, type: "recipient", label: `To: ${target}` });
      }
      chips.push({ id: "trans-dispatched", type: "transition", label: "Dispatched", status: "completed" });
    }
  }

  // 2. Routing action
  const actionRaw = String(
    payload.action ||
      (isRecord(payload.routing) ? payload.routing.action : "") ||
      "",
  ).trim();

  if (actionRaw) {
    const act = actionRaw.toLowerCase();
    if (act === "delegate" || act === "create") {
      chips.push({ id: "routing-delegate", type: "routing", label: "Routing: Delegate" });
    } else if (act === "steer") {
      chips.push({ id: "routing-steer", type: "routing", label: "Routing: Steer" });
    } else if (act === "stop" || act === "interrupt") {
      chips.push({ id: "routing-stop", type: "routing", label: "Routing: Stop" });
    } else if (
      act !== "reply" &&
      act !== "direct_reply" &&
      act !== "direct reply" &&
      act !== "directreply"
    ) {
      const label = actionRaw.startsWith("Routing:") ? actionRaw : `Routing: ${actionRaw}`;
      chips.push({ id: `routing-${act}`, type: "routing", label });
    }
  }

  // 3. Target recipient
  const targetRaw = String(
    payload.peerProfileName ||
      payload.targetAgent ||
      payload.recipient ||
      payload.targetProfile ||
      payload.agent ||
      (isRecord(payload.routing)
        ? payload.routing.peerProfileName ||
          payload.routing.targetAgent ||
          payload.routing.recipient ||
          payload.routing.targetProfile
        : "") ||
      "",
  ).trim();

  if (targetRaw) {
    const label =
      targetRaw.startsWith("To:") || targetRaw.startsWith("Recipient:")
        ? targetRaw
        : `To: ${targetRaw}`;
    chips.push({ id: `to-${targetRaw}`, type: "recipient", label });
  }

  // 4. Run name
  const runNameRaw = String(
    payload.runName ||
      payload.agentRun ||
      (isRecord(payload.routing) ? payload.routing.runName : "") ||
      "",
  ).trim();
  if (runNameRaw && !chips.some((c) => c.label.includes(runNameRaw))) {
    chips.push({
      id: `run-${runNameRaw}`,
      type: "transition",
      label: `Starting AgentRun: ${runNameRaw}`,
      status: "running",
    });
  }

  // 5. Transitions / status / phase
  const rawTransitions = [
    ...(Array.isArray(payload.transitions) ? payload.transitions : []),
    ...(isRecord(payload.routing) && Array.isArray(payload.routing.transitions)
      ? payload.routing.transitions
      : []),
    payload.transition,
    payload.status,
    payload.phase,
    payload.state,
  ].filter((t): t is string => typeof t === "string" && Boolean(t.trim()));

  for (const trans of rawTransitions) {
    const tr = trans.trim();
    if (!tr) continue;
    const lower = tr.toLowerCase();
    const turn = turnStatusFromRaw(lower);
    const status: ChatChipStatus =
      turn === "failed"
        ? "failed"
        : turn === "running"
        ? "running"
        : turn === "queued" || turn === "waiting"
        ? "pending"
        : lower.includes("dispatch") || lower.includes("complete") || lower.includes("delegat")
        ? "completed"
        : "info";
    chips.push({
      id: `trans-${lower.replace(/[^a-z0-9]+/g, "-")}`,
      type: "transition",
      label: tr,
      status,
    });
  }

  // 6. EventName handling
  if (eventName && eventName !== "message") {
    const ev = eventName.toLowerCase();
    if (ev === "dispatched" || ev === "running" || ev === "delegated" || ev === "starting") {
      const cap = ev.charAt(0).toUpperCase() + ev.slice(1);
      chips.push({ id: `event-${ev}`, type: "transition", label: cap });
    }
  }

  return dedupeChips(chips);
}

export function extractChipsAndCleanText(rawText: string): { text: string; chips: ChatChip[] } {
  let text = rawText || "";
  const chips: ChatChip[] = [];

  // Match bracket directives: [Routing: Delegate], [To: scout], [Starting AgentRun: scout-1], [Dispatched], [Running]
  const bracketRegex =
    /\[(Routing:\s*[^\]]+|(?:To|Recipient):\s*[^\]]+|Starting AgentRun:\s*[^\]]+|Dispatched|Running|Completed|Failed|Delegated)\]/gi;
  let match: RegExpExecArray | null;
  while ((match = bracketRegex.exec(text)) !== null) {
    const item = match[1].trim();
    if (/^routing:/i.test(item)) {
      chips.push({ id: `routing-${chips.length}`, type: "routing", label: item });
    } else if (/^(to|recipient):/i.test(item)) {
      const target = item.replace(/^(to|recipient):\s*/i, "").trim();
      chips.push({ id: `to-${chips.length}`, type: "recipient", label: `To: ${target}` });
    } else {
      chips.push({ id: `trans-${chips.length}`, type: "transition", label: item });
    }
  }
  if (chips.length > 0) {
    text = text.replace(bracketRegex, "").trim();
  }

  // Match header preamble:
  // Routing: Delegate
  // To: scout
  // Transition: Dispatched
  const linePreambleRegex =
    /^(?:Routing:\s*([^\n\r]+)[\n\r]+)?(?:(?:To|Recipient):\s*([^\n\r]+)[\n\r]+)?(?:(?:Starting AgentRun|Status|Transition|State):\s*([^\n\r]+)[\n\r]+)?/i;
  const preambleMatch = text.match(linePreambleRegex);
  if (preambleMatch && (preambleMatch[1] || preambleMatch[2] || preambleMatch[3])) {
    if (preambleMatch[1] && !chips.some((c) => c.type === "routing")) {
      chips.push({
        id: "routing-preamble",
        type: "routing",
        label: preambleMatch[1].startsWith("Routing:")
          ? preambleMatch[1].trim()
          : `Routing: ${preambleMatch[1].trim()}`,
      });
    }
    if (preambleMatch[2] && !chips.some((c) => c.type === "recipient")) {
      chips.push({
        id: "to-preamble",
        type: "recipient",
        label: preambleMatch[2].startsWith("To:")
          ? preambleMatch[2].trim()
          : `To: ${preambleMatch[2].trim()}`,
      });
    }
    if (preambleMatch[3]) {
      const st = preambleMatch[3].trim();
      if (!chips.some((c) => c.label.toLowerCase() === st.toLowerCase())) {
        chips.push({ id: "trans-preamble", type: "transition", label: st });
      }
    }
    text = text.slice(preambleMatch[0].length).trim();
  }

  return { text, chips: dedupeChips(chips) };
}

export function extractChipsFromMessage(message: ChatMessage | undefined): ChatChip[] {
  if (!message) {
    return [];
  }
  const chips: ChatChip[] = [];
  if (Array.isArray(message.chips)) {
    chips.push(...message.chips);
  }
  if (isRecord(message.metadata)) {
    chips.push(...extractChipsFromPayload(message.metadata));
  }
  const { chips: textChips } = extractChipsAndCleanText(message.content || "");
  chips.push(...textChips);
  return dedupeChips(chips);
}


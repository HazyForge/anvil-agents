export const READY_CONDITION = "Ready";
export const INTERRUPT_DUPLICATE_REASON = "InterruptDuplicate";
export const INTERRUPT_DUPLICATE_ERROR_PREFIX = "interruptDuplicate:";

export type AgentRunCondition = {
  type?: string;
  status?: string;
  reason?: string;
  message?: string;
};

export type AgentRunStatusSlice = {
  name?: string;
  namespace?: string;
  phase?: string;
  backend?: string;
  error?: string;
  conditions?: AgentRunCondition[];
  decision?: { action?: string; summary?: string };
};

export function readyCondition(run?: AgentRunStatusSlice | null): AgentRunCondition | undefined {
  return (run?.conditions ?? []).find((condition) => (condition.type || "").trim() === READY_CONDITION);
}

export function readyReason(run?: AgentRunStatusSlice | null): string {
  return (readyCondition(run)?.reason || "").trim();
}

export function interruptDuplicateError(run?: AgentRunStatusSlice | null): string {
  const error = (run?.error || "").trim();
  return error.startsWith(INTERRUPT_DUPLICATE_ERROR_PREFIX) ? error : "";
}

/** True when GET/stream has already shown A Failed / InterruptDuplicate. */
export function isInterruptDuplicateHold(run?: AgentRunStatusSlice | null): boolean {
  if (!run) {
    return false;
  }
  if ((run.phase || "").trim() !== "Failed") {
    return false;
  }
  return readyReason(run) === INTERRUPT_DUPLICATE_REASON || interruptDuplicateError(run) !== "";
}

function withReadyInterruptDuplicate(
  preferred?: AgentRunCondition[],
  fallback?: AgentRunCondition[],
): AgentRunCondition[] | undefined {
  const fromPreferred = (preferred ?? []).find(
    (condition) =>
      (condition.type || "").trim() === READY_CONDITION &&
      (condition.reason || "").trim() === INTERRUPT_DUPLICATE_REASON,
  );
  const fromFallback = (fallback ?? []).find(
    (condition) =>
      (condition.type || "").trim() === READY_CONDITION &&
      (condition.reason || "").trim() === INTERRUPT_DUPLICATE_REASON,
  );
  const ready = fromPreferred ?? fromFallback;
  if (!ready) {
    return preferred ?? fallback;
  }
  const rest = (preferred ?? fallback ?? []).filter(
    (condition) => (condition.type || "").trim() !== READY_CONDITION,
  );
  return [ready, ...rest];
}

function heldError(preferred?: string, fallback?: string): string | undefined {
  const first = (preferred || "").trim();
  const second = (fallback || "").trim();
  if (first.startsWith(INTERRUPT_DUPLICATE_ERROR_PREFIX)) {
    return first;
  }
  if (second.startsWith(INTERRUPT_DUPLICATE_ERROR_PREFIX)) {
    return second;
  }
  return first || second || undefined;
}

/**
 * Keep Failed / Ready InterruptDuplicate / interruptDuplicate: error once seen.
 * A later Running status patch (Job still alive) must not replace those CR fields.
 */
export function stickAgentRunStatus<T extends AgentRunStatusSlice>(
  previous: T | null | undefined,
  next: T,
): T {
  if (!isInterruptDuplicateHold(previous)) {
    return next;
  }
  return {
    ...next,
    name: next.name || previous?.name || "",
    namespace: next.namespace || previous?.namespace || "",
    phase: "Failed",
    error: heldError(previous?.error, next.error),
    conditions: withReadyInterruptDuplicate(previous?.conditions, next.conditions),
  };
}
